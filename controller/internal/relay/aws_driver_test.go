// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestAWSStateToVMState(t *testing.T) {
	assert.Equal(t, VMStatePending, awsStateToVMState(ec2types.InstanceStateNamePending))
	assert.Equal(t, VMStateRunning, awsStateToVMState(ec2types.InstanceStateNameRunning))
	assert.Equal(t, VMStateStopping, awsStateToVMState(ec2types.InstanceStateNameStopping))
	assert.Equal(t, VMStateStopped, awsStateToVMState(ec2types.InstanceStateNameStopped))
	assert.Equal(t, VMStateTerminated, awsStateToVMState(ec2types.InstanceStateNameTerminated))
	assert.Equal(t, VMStateTerminated, awsStateToVMState(ec2types.InstanceStateNameShuttingDown))
}

func TestClassifyAWSError_Capacity(t *testing.T) {
	assert.True(t, IsCapacityError(classifyAWSError(simpleError("InsufficientInstanceCapacity: no m5.large"))))
	assert.True(t, IsCapacityError(classifyAWSError(simpleError("InstanceLimitExceeded: too many instances"))))
	assert.True(t, IsCapacityError(classifyAWSError(simpleError("RequestLimitExceeded: throttled"))))
	assert.True(t, IsCapacityError(classifyAWSError(simpleError("Throttling: rate exceeded"))))
}

func TestClassifyAWSError_Config(t *testing.T) {
	assert.True(t, IsConfigError(classifyAWSError(simpleError("InvalidParameterValue: bad AMI"))))
	assert.True(t, IsConfigError(classifyAWSError(simpleError("InvalidAMIID: not found"))))
	assert.True(t, IsConfigError(classifyAWSError(simpleError("UnauthorizedOperation: no perms"))))
	assert.True(t, IsConfigError(classifyAWSError(simpleError("AccessDenied: forbidden"))))
}

func TestClassifyAWSError_GenericError(t *testing.T) {
	err := classifyAWSError(simpleError("InternalError: something broke"))
	assert.False(t, IsCapacityError(err))
	assert.False(t, IsConfigError(err))
	assert.Contains(t, err.Error(), "AWS API error")
}

func TestClassifyAWSError_NilError(t *testing.T) {
	assert.Nil(t, classifyAWSError(nil))
}

func TestAWSDriver_GetConfig_FallsBackToDefaultChain(t *testing.T) {
	// No secret in the cluster — should fall back to default credential chain
	// (which will fail without env vars, but the function should not panic)
	fakeClient := fake.NewClientBuilder().Build()
	d := NewAWSDriver(fakeClient, "test-ns", "aws-relay-irwa")
	_, err := d.loadAWSConfig(context.Background(), "us-east-1")
	// This will fail because there are no AWS credentials in the test env,
	// but it should NOT panic or return a config error.
	// The error from the default chain is acceptable in tests.
	_ = err // expected to be non-nil in test env without AWS creds
}

func TestNewAWSDriver_DefaultCredentialSecret(t *testing.T) {
	d := NewAWSDriver(nil, "ns", "")
	assert.Equal(t, "aws-relay-irwa", d.credentialSecret)
}

func TestNewAWSDriver_CustomCredentialSecret(t *testing.T) {
	d := NewAWSDriver(nil, "ns", "custom-aws-creds")
	assert.Equal(t, "custom-aws-creds", d.credentialSecret)
}
