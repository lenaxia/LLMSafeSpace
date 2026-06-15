// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AWSDriver implements ProviderDriver for AWS EC2.
// It provisions relay VMs using RunInstances, manages lifecycle via
// TerminateInstances/DescribeInstances. Credentials are read from a
// K8s Secret (accessKeyId, secretAccessKey) or the default credential chain
// (IRSA, env vars, instance metadata).
type AWSDriver struct {
	k8sClient        client.Client
	namespace        string
	credentialSecret string
}

// NewAWSDriver creates an AWS EC2 driver that reads credentials from the
// named K8s Secret (default: aws-relay-irwa).
func NewAWSDriver(k8sClient client.Client, namespace, credentialSecret string) *AWSDriver {
	if credentialSecret == "" {
		credentialSecret = "aws-relay-irwa"
	}
	return &AWSDriver{
		k8sClient:        k8sClient,
		namespace:        namespace,
		credentialSecret: credentialSecret,
	}
}

// loadAWSConfig builds an AWS config for the given region, using credentials
// from the K8s Secret if available, otherwise the default credential chain.
func (d *AWSDriver) loadAWSConfig(ctx context.Context, region string) (aws.Config, error) {
	secret := &corev1.Secret{}
	if err := d.k8sClient.Get(ctx,
		types.NamespacedName{Name: d.credentialSecret, Namespace: d.namespace},
		secret); err != nil {
		// Fall back to default credential chain (IRSA, env, instance metadata)
		return awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	}

	accessKey := string(secret.Data["accessKeyId"])
	secretKey := string(secret.Data["secretAccessKey"])

	if accessKey != "" && secretKey != "" {
		// Use explicit credentials from the Secret
		return awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithRegion(region),
			awsconfig.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
			),
		)
	}

	// Fall back to default credential chain
	return awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
}

// Provision creates an EC2 instance with the given cloud-init userdata.
func (d *AWSDriver) Provision(ctx context.Context, req ProvisionRequest) (*ProvisionResult, error) {
	region := req.Region
	if region == "" {
		region = defaultRegionAWS
	}

	shape := req.Shape
	if shape == "" {
		shape = defaultShapeAWS
	}

	cfg, err := d.loadAWSConfig(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("%w: load AWS config: %v", ErrConfig, err)
	}

	client := ec2.NewFromConfig(cfg)

	// Resolve the latest Ubuntu ARM64 AMI for this region
	imageID, err := d.resolveAMI(ctx, client, region)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve AMI: %v", ErrConfig, err)
	}

	// Run instances
	runOutput, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String(imageID),
		InstanceType: ec2types.InstanceType(shape),
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		UserData:     aws.String(req.CloudInit),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeInstance,
				Tags: []ec2types.Tag{
					{Key: aws.String("Name"), Value: aws.String(req.Name)},
					{Key: aws.String("managed-by"), Value: aws.String("llmsafespace-relay")},
				},
			},
		},
	})
	if err != nil {
		return nil, classifyAWSError(err)
	}
	if len(runOutput.Instances) == 0 {
		return nil, fmt.Errorf("%w: RunInstances returned no instances", ErrConfig)
	}

	instanceID := aws.ToString(runOutput.Instances[0].InstanceId)

	// Wait for the instance to be running
	publicIP, err := d.waitForRunning(ctx, client, instanceID)
	if err != nil {
		return nil, err
	}

	return &ProvisionResult{
		InstanceID: instanceID,
		PublicIP:   publicIP,
	}, nil
}

// Destroy terminates an EC2 instance.
func (d *AWSDriver) Destroy(ctx context.Context, instanceID, region string) error {
	destroyRegion := region
	if destroyRegion == "" {
		destroyRegion = defaultRegionAWS
	}

	cfg, err := d.loadAWSConfig(ctx, destroyRegion)
	if err != nil {
		return fmt.Errorf("%w: load AWS config: %v", ErrConfig, err)
	}

	client := ec2.NewFromConfig(cfg)
	_, err = client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return classifyAWSError(err)
	}
	return nil
}

// GetStatus returns the current state of an EC2 instance.
func (d *AWSDriver) GetStatus(ctx context.Context, instanceID, region string) (*VMStatus, error) {
	statusRegion := region
	if statusRegion == "" {
		statusRegion = defaultRegionAWS
	}

	cfg, err := d.loadAWSConfig(ctx, statusRegion)
	if err != nil {
		return nil, fmt.Errorf("%w: load AWS config: %v", ErrConfig, err)
	}

	client := ec2.NewFromConfig(cfg)
	output, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return nil, classifyAWSError(err)
	}

	for _, reservation := range output.Reservations {
		for _, inst := range reservation.Instances {
			if aws.ToString(inst.InstanceId) == instanceID {
				return &VMStatus{
					InstanceID: instanceID,
					State:      awsStateToVMState(inst.State.Name),
					PublicIP:   aws.ToString(inst.PublicIpAddress),
				}, nil
			}
		}
	}

	return &VMStatus{InstanceID: instanceID, State: VMStateNotFound}, nil
}

// ListInstances returns relay VMs managed by this driver.
func (d *AWSDriver) ListInstances(ctx context.Context, region string) ([]VMInstance, error) {
	listRegion := region
	if listRegion == "" {
		listRegion = defaultRegionAWS
	}

	cfg, err := d.loadAWSConfig(ctx, listRegion)
	if err != nil {
		return nil, fmt.Errorf("%w: load AWS config: %v", ErrConfig, err)
	}

	client := ec2.NewFromConfig(cfg)
	output, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("tag:managed-by"),
				Values: []string{"llmsafespace-relay"},
			},
		},
	})
	if err != nil {
		return nil, classifyAWSError(err)
	}

	result := make([]VMInstance, 0)
	for _, reservation := range output.Reservations {
		for _, inst := range reservation.Instances {
			result = append(result, VMInstance{
				InstanceID: aws.ToString(inst.InstanceId),
				PublicIP:   aws.ToString(inst.PublicIpAddress),
				State:      awsStateToVMState(inst.State.Name),
			})
		}
	}
	return result, nil
}

// resolveAMI finds the latest Ubuntu 22.04 ARM64 AMI for the region.
func (d *AWSDriver) resolveAMI(ctx context.Context, client *ec2.Client, region string) (string, error) {
	output, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Owners: []string{"099720109477"}, // Canonical
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("name"),
				Values: []string{"ubuntu/images/hvm-ssd-generig-arm64-ubuntu-jammy-22.04*"},
			},
			{
				Name:   aws.String("architecture"),
				Values: []string{"arm64"},
			},
			{
				Name:   aws.String("state"),
				Values: []string{"available"},
			},
		},
	})
	if err != nil {
		return "", classifyAWSError(err)
	}

	if len(output.Images) == 0 {
		return "", fmt.Errorf("no Ubuntu ARM64 AMI found for region %s", region)
	}

	// Pick the most recent image
	var latestAMI *ec2types.Image
	for i := range output.Images {
		if latestAMI == nil ||
			aws.ToInt64(output.Images[i].CreationDate) > aws.ToInt64(latestAMI.CreationDate) {
			latestAMI = &output.Images[i]
		}
	}

	if latestAMI == nil {
		return "", fmt.Errorf("no valid AMI found")
	}

	return aws.ToString(latestAMI.ImageId), nil
}

// waitForRunning polls the instance until it's running, then returns its public IP.
func (d *AWSDriver) waitForRunning(ctx context.Context, client *ec2.Client, instanceID string) (string, error) {
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(10 * time.Second):
		}

		output, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{instanceID},
		})
		if err != nil {
			return "", classifyAWSError(err)
		}

		for _, reservation := range output.Reservations {
			for _, inst := range reservation.Instances {
				if aws.ToString(inst.InstanceId) != instanceID {
					continue
				}
				state := inst.State
				if state == nil {
					continue
				}
				switch state.Name {
				case ec2types.InstanceStateNameRunning:
					ip := aws.ToString(inst.PublicIpAddress)
					if ip != "" {
						return ip, nil
					}
					// Running but no public IP yet — wait a bit more
				case ec2types.InstanceStateNameTerminated:
					return "", fmt.Errorf("%w: instance terminated during provisioning", ErrConfig)
				case ec2types.InstanceStateNameStopped:
					return "", fmt.Errorf("%w: instance stopped during provisioning", ErrConfig)
				}
			}
		}
	}
	return "", ErrTimeout
}

// awsStateToVMState maps EC2 instance states to VMState.
func awsStateToVMState(state ec2types.InstanceStateName) VMState {
	switch state {
	case ec2types.InstanceStateNamePending:
		return VMStatePending
	case ec2types.InstanceStateNameRunning:
		return VMStateRunning
	case ec2types.InstanceStateNameStopping:
		return VMStateStopping
	case ec2types.InstanceStateNameStopped:
		return VMStateStopped
	case ec2types.InstanceStateNameShuttingDown, ec2types.InstanceStateNameTerminated:
		return VMStateTerminated
	default:
		return VMStatePending
	}
}

// classifyAWSError maps AWS SDK errors to typed errors for circuit breaker logic.
func classifyAWSError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "InsufficientInstanceCapacity") || strings.Contains(msg, "InstanceLimitExceeded") {
		return fmt.Errorf("%w: %s", ErrCapacity, msg)
	}
	if strings.Contains(msg, "RequestLimitExceeded") || strings.Contains(msg, "Throttling") {
		return fmt.Errorf("%w: AWS rate limited", ErrCapacity)
	}
	if strings.Contains(msg, "InvalidParameterValue") || strings.Contains(msg, "InvalidAMIID") ||
		strings.Contains(msg, "UnauthorizedOperation") || strings.Contains(msg, "AccessDenied") {
		return fmt.Errorf("%w: %s", ErrConfig, msg)
	}
	return fmt.Errorf("AWS API error: %s", msg)
}
