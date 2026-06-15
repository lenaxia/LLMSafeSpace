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

type testErr struct{ msg string }

func (e testErr) Error() string { return e.msg }

func TestAWSStateToVMState(t *testing.T) {
	assert.Equal(t, VMStatePending, awsStateToVMState(ec2types.InstanceStateNamePending))
	assert.Equal(t, VMStateRunning, awsStateToVMState(ec2types.InstanceStateNameRunning))
	assert.Equal(t, VMStateStopping, awsStateToVMState(ec2types.InstanceStateNameStopping))
	assert.Equal(t, VMStateStopped, awsStateToVMState(ec2types.InstanceStateNameStopped))
	assert.Equal(t, VMStateTerminated, awsStateToVMState(ec2types.InstanceStateNameTerminated))
	assert.Equal(t, VMStateTerminated, awsStateToVMState(ec2types.InstanceStateNameShuttingDown))
}

func TestClassifyAWSError_Capacity(t *testing.T) {
	assert.True(t, IsCapacityError(classifyAWSError(testErr{"InsufficientInstanceCapacity: no m5.large"})))
	assert.True(t, IsCapacityError(classifyAWSError(testErr{"InstanceLimitExceeded: too many instances"})))
	assert.True(t, IsCapacityError(classifyAWSError(testErr{"RequestLimitExceeded: throttled"})))
	assert.True(t, IsCapacityError(classifyAWSError(testErr{"Throttling: rate exceeded"})))
}

func TestClassifyAWSError_Config(t *testing.T) {
	assert.True(t, IsConfigError(classifyAWSError(testErr{"InvalidParameterValue: bad AMI"})))
	assert.True(t, IsConfigError(classifyAWSError(testErr{"InvalidAMIID: not found"})))
	assert.True(t, IsConfigError(classifyAWSError(testErr{"UnauthorizedOperation: no perms"})))
	assert.True(t, IsConfigError(classifyAWSError(testErr{"AccessDenied: forbidden"})))
}

func TestClassifyAWSError_GenericError(t *testing.T) {
	err := classifyAWSError(testErr{"InternalError: something broke"})
	assert.False(t, IsCapacityError(err))
	assert.False(t, IsConfigError(err))
	assert.Contains(t, err.Error(), "AWS API error")
}

func TestClassifyAWSError_NilError(t *testing.T) {
	assert.Nil(t, classifyAWSError(nil))
}

func TestAWSDriver_GetConfig_FallsBackToDefaultChain(t *testing.T) {
	fakeClient := fake.NewClientBuilder().Build()
	d := NewAWSDriver(fakeClient, "test-ns", "aws-relay-irwa")
	cfg, err := d.loadAWSConfig(context.Background(), "us-east-1")
	// Expected to succeed (falls back to default chain) or fail (no AWS creds in test)
	// but should NOT panic. If it succeeds, region should match.
	if err == nil {
		assert.Equal(t, "us-east-1", cfg.Region)
	}
}

func TestNewAWSDriver_DefaultCredentialSecret(t *testing.T) {
	d := NewAWSDriver(nil, "ns", "")
	assert.Equal(t, "aws-relay-irwa", d.credentialSecret)
}

func TestNewAWSDriver_CustomCredentialSecret(t *testing.T) {
	d := NewAWSDriver(nil, "ns", "custom-aws-creds")
	assert.Equal(t, "custom-aws-creds", d.credentialSecret)
}
