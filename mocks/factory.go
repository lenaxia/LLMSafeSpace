// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package mocks

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	lmocks "github.com/lenaxia/llmsafespace/mocks/logger"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// MockFactory creates test fixtures.
type MockFactory struct{}

func NewMockFactory() *MockFactory { return &MockFactory{} }

func (f *MockFactory) NewLogger() *lmocks.MockLogger {
	return lmocks.NewMockLogger()
}

func (f *MockFactory) NewRuntimeEnvironment(name, language, version string) *v1.RuntimeEnvironment {
	return &v1.RuntimeEnvironment{
		TypeMeta:   metav1.TypeMeta{Kind: "RuntimeEnvironment", APIVersion: "llmsafespace.dev/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: "test-uid"},
		Spec: v1.RuntimeEnvironmentSpec{
			Image:    "llmsafespace/" + language + ":" + version,
			Language: language,
			Version:  version,
		},
		Status: v1.RuntimeEnvironmentStatus{Available: true},
	}
}
