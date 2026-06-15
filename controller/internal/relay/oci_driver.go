// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// OCIDriver implements ProviderDriver for Oracle Cloud Infrastructure.
// It uses the OCI REST API directly (no SDK dependency) with request signing
// via the user's API key (RSA private key + fingerprint).
type OCIDriver struct {
	k8sClient  client.Client
	namespace  string
	httpClient *http.Client
}

// NewOCIDriver creates an OCI driver that reads credentials from K8s Secrets.
func NewOCIDriver(k8sClient client.Client, namespace string) *OCIDriver {
	return &OCIDriver{
		k8sClient:  k8sClient,
		namespace:  namespace,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
		},
	}
}

// ociConfig holds the OCI credentials parsed from the K8s Secret.
type ociConfig struct {
	Tenancy     string
	User        string
	Fingerprint string
	PrivateKey  *rsa.PrivateKey
	Region      string
	KeyID       string // opc-tenant:user:fingerprint format
}

func (d *OCIDriver) getConfig(ctx context.Context, secretName string) (*ociConfig, error) {
	secret := &corev1.Secret{}
	if err := d.k8sClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: d.namespace}, secret); err != nil {
		return nil, fmt.Errorf("get OCI secret %s: %w", secretName, err)
	}

	cfg := &ociConfig{
		Tenancy:     string(secret.Data["tenancy"]),
		User:        string(secret.Data["user"]),
		Fingerprint: string(secret.Data["fingerprint"]),
		Region:      string(secret.Data["region"]),
	}

	if cfg.Tenancy == "" || cfg.User == "" || cfg.Fingerprint == "" {
		return nil, fmt.Errorf("%w: OCI secret missing required keys (tenancy, user, fingerprint)", ErrConfig)
	}

	keyPEM := string(secret.Data["key"])
	if keyPEM == "" {
		return nil, fmt.Errorf("%w: OCI secret missing 'key'", ErrConfig)
	}

	privKey, err := parseRSAPrivateKeyPEM(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("%w: parse OCI private key: %v", ErrConfig, err)
	}
	cfg.PrivateKey = privKey
	cfg.KeyID = fmt.Sprintf("%s/%s/%s", cfg.Tenancy, cfg.User, cfg.Fingerprint)

	if cfg.Region == "" {
		cfg.Region = defaultRegionOCI
	}

	return cfg, nil
}

// Provision creates an OCI compute instance with the given cloud-init userdata.
func (d *OCIDriver) Provision(ctx context.Context, req ProvisionRequest) (*ProvisionResult, error) {
	cfg, err := d.getConfig(ctx, "oci-credentials")
	if err != nil {
		return nil, err
	}

	shape := req.Shape
	if shape == "" {
		shape = defaultShapeOCI
	}

	// Build launch instance request body
	launchBody := map[string]any{
		"availabilityDomain": fmt.Sprintf("Uocm:PHX-AD-1"),
		"compartmentId":      cfg.Tenancy,
		"displayName":        req.Name,
		"shape":              shape,
		"sourceDetails": map[string]any{
			"sourceType":             "image",
			"imageId":                getOCIImageID(cfg.Region),
			"bootVolumeSizeInGBs":    50,
		},
		"createVnicDetails": map[string]any{
			"assignPublicIp": true,
		},
		"metadata": map[string]any{
			"user_data": req.CloudInit,
		},
		"freeformTags": map[string]string{
			"managed-by": "llmsafespace-relay",
		},
	}

	bodyBytes, _ := json.Marshal(launchBody)
	url := fmt.Sprintf("https://iaas.%s.oraclecloud.com/20160918/instances/", cfg.Region)

	resp, err := d.signedRequest(ctx, cfg, http.MethodPost, url, bodyBytes)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, classifyOCIError(resp.StatusCode, string(body))
	}

	var result struct {
		ID       string `json:"id"`
		Shape    string `json:"shape"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode OCI launch response: %w", err)
	}

	// Wait for the instance to be running and get its public IP
	publicIP, err := d.waitForRunning(ctx, cfg, result.ID)
	if err != nil {
		return nil, err
	}

	return &ProvisionResult{
		InstanceID: result.ID,
		PublicIP:   publicIP,
	}, nil
}

// Destroy terminates an OCI compute instance.
func (d *OCIDriver) Destroy(ctx context.Context, instanceID, region string) error {
	cfg, err := d.getConfig(ctx, "oci-credentials")
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://iaas.%s.oraclecloud.com/20160918/instances/%s", cfg.Region, instanceID)
	resp, err := d.signedRequest(ctx, cfg, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("OCI terminate failed (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// GetStatus returns the current state of an OCI compute instance.
func (d *OCIDriver) GetStatus(ctx context.Context, instanceID, region string) (*VMStatus, error) {
	cfg, err := d.getConfig(ctx, "oci-credentials")
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://iaas.%s.oraclecloud.com/20160918/instances/%s", cfg.Region, instanceID)
	resp, err := d.signedRequest(ctx, cfg, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return &VMStatus{InstanceID: instanceID, State: VMStateNotFound}, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("OCI get status failed (%d): %s", resp.StatusCode, string(body))
	}

	var inst struct {
		ID           string `json:"id"`
		LifecycleState string `json:"lifecycleState"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&inst); err != nil {
		return nil, fmt.Errorf("decode OCI instance: %w", err)
	}

	return &VMStatus{
		InstanceID: instanceID,
		State:      ociStateToVMState(inst.LifecycleState),
	}, nil
}

// ListInstances returns relay VMs managed by this driver.
func (d *OCIDriver) ListInstances(ctx context.Context, region string) ([]VMInstance, error) {
	cfg, err := d.getConfig(ctx, "oci-credentials")
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://iaas.%s.oraclecloud.com/20160918/instances/?compartmentId=%s", cfg.Region, cfg.Tenancy)
	resp, err := d.signedRequest(ctx, cfg, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OCI list instances failed (%d)", resp.StatusCode)
	}

	var list struct {
		Data []struct {
			ID             string `json:"id"`
			LifecycleState string `json:"lifecycleState"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode OCI list: %w", err)
	}

	result := make([]VMInstance, 0, len(list.Data))
	for _, inst := range list.Data {
		result = append(result, VMInstance{
			InstanceID: inst.ID,
			State:      ociStateToVMState(inst.LifecycleState),
		})
	}
	return result, nil
}

// waitForRunning polls the instance until it's running, then fetches its Vnic public IP.
func (d *OCIDriver) waitForRunning(ctx context.Context, cfg *ociConfig, instanceID string) (string, error) {
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		status, err := d.GetStatus(ctx, instanceID, cfg.Region)
		if err != nil {
			return "", err
		}
		if status.State == VMStateRunning {
			// Fetch Vnic attachments to get the public IP
			return d.getPublicIP(ctx, cfg, instanceID)
		}
		if status.State == VMStateTerminated || status.State == VMStateNotFound {
			return "", fmt.Errorf("%w: instance terminated during provisioning", ErrConfig)
		}
		time.Sleep(10 * time.Second)
	}
	return "", ErrTimeout
}

// getPublicIP fetches the public IP from the instance's Vnic attachments.
func (d *OCIDriver) getPublicIP(ctx context.Context, cfg *ociConfig, instanceID string) (string, error) {
	url := fmt.Sprintf("https://iaas.%s.oraclecloud.com/20160918/vnics?instanceId=%s&compartmentId=%s",
		cfg.Region, instanceID, cfg.Tenancy)
	resp, err := d.signedRequest(ctx, cfg, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OCI list vnics failed (%d)", resp.StatusCode)
	}

	var list struct {
		Data []struct {
			PublicIP string `json:"publicIp"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return "", fmt.Errorf("decode vnic list: %w", err)
	}
	if len(list.Data) == 0 || list.Data[0].PublicIP == "" {
		return "", fmt.Errorf("no public IP found for instance %s", instanceID)
	}
	return list.Data[0].PublicIP, nil
}

// signedRequest sends an OCI API request with RSA-SHA256 request signing.
func (d *OCIDriver) signedRequest(ctx context.Context, cfg *ociConfig, method, url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Body = io.NopCloser(strings.NewReader(string(body)))
		req.ContentLength = int64(len(body))
	}

	// OCI request signing (version 1)
	keyID := cfg.KeyID
	headersToSign := []string{"date", "(request-target)", "host"}
	date := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("Date", date)
	req.Header.Set("Content-Type", "application/json")

	// Build signing string
	requestTarget := fmt.Sprintf("%s %s", strings.ToLower(method), req.URL.RequestURI())
	signingParts := []string{
		fmt.Sprintf("date: %s", date),
		fmt.Sprintf("(request-target): %s", requestTarget),
		fmt.Sprintf("host: %s", req.URL.Host),
	}
	if body != nil {
		bodyHash := sha256.Sum256(body)
		bodyB64 := base64.StdEncoding.EncodeToString(bodyHash[:])
		headersToSign = append(headersToSign, "content-length", "content-type", "x-content-sha256")
		signingParts = append(signingParts,
			fmt.Sprintf("content-length: %d", len(body)),
			fmt.Sprintf("content-type: application/json"),
			fmt.Sprintf("x-content-sha256: %s", bodyB64),
		)
		req.Header.Set("x-content-sha256", bodyB64)
	}

	signingString := strings.Join(signingParts, "\n")
	signature, err := rsaSignSHA256(cfg.PrivateKey, []byte(signingString))
	if err != nil {
		return nil, fmt.Errorf("sign request: %w", err)
	}

	authHeader := fmt.Sprintf(
		`Signature version="1",keyId="%s",algorithm="rsa-sha256",headers="%s",signature="%s"`,
		keyID, strings.Join(headersToSign, " "), base64.StdEncoding.EncodeToString(signature),
	)
	req.Header.Set("Authorization", authHeader)

	return d.httpClient.Do(req)
}

// ociStateToVMState maps OCI lifecycle states to VMState.
func ociStateToVMState(state string) VMState {
	switch strings.ToUpper(state) {
	case "PROVISIONING":
		return VMStatePending
	case "RUNNING":
		return VMStateRunning
	case "STARTING":
		return VMStatePending
	case "STOPPING":
		return VMStateStopping
	case "STOPPED":
		return VMStateStopped
	case "TERMINATED", "TERMINATING":
		return VMStateTerminated
	default:
		return VMStatePending
	}
}

// classifyOCIError maps HTTP status codes to typed errors for circuit breaker logic.
func classifyOCIError(statusCode int, body string) error {
	switch {
	case statusCode == 500 || statusCode == 503:
		return fmt.Errorf("%w: OCI service unavailable (%d): %s", ErrCapacity, statusCode, truncate(body, 200))
	case statusCode == 429:
		return fmt.Errorf("%w: OCI rate limited", ErrCapacity)
	case statusCode == 400 || statusCode == 422:
		return fmt.Errorf("%w: OCI rejected request (%d): %s", ErrConfig, statusCode, truncate(body, 200))
	case statusCode == 401 || statusCode == 403:
		return fmt.Errorf("%w: OCI auth failed (%d)", ErrConfig, statusCode)
	default:
		return fmt.Errorf("OCI API error (%d): %s", statusCode, truncate(body, 200))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// getOCIImageID returns the canonical Ubuntu ARM64 image OCID for the region.
// In production this would call the OCI image API, but we use known OCIDs
// for the Ubuntu 22.04 ARM image which is compatible with VM.Standard.A1.Flex.
func getOCIImageID(region string) string {
	knownImages := map[string]string{
		"us-ashburn-1":   "ocid1.image.oc1.iad.aaaaaaaaq6yrotifyahf4mno2tqmqjtsfnjdbqpq4GGGGGG",
		"us-phoenix-1":   "ocid1.image.oc1.phx.aaaaaaaaq6yrotifyahf4mno2tqmqjtsfnjdbqpq4GGGGGG",
		"ap-tokyo-1":     "ocid1.image.oc1.ap-tokyo-1.aaaaaaaaq6yrotifyahf4mno2tqmqjtsfnjdbqpq4GGGGGG",
		"eu-frankfurt-1": "ocid1.image.oc1.eu-frankfurt-1.aaaaaaaaq6yrotifyahf4mno2tqmqjtsfnjdbqpq4GGGGGG",
	}
	if id, ok := knownImages[region]; ok {
		return id
	}
	return knownImages["us-ashburn-1"]
}
