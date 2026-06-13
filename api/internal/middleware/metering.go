// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package middleware

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

type meteringBucket struct {
	read  int64
	write int64
}

type MeteringMiddleware struct {
	meteringSvc interfaces.MeteringService
	mu          sync.Mutex
	buckets     map[string]*meteringBucket
	stopCh      chan struct{}
}

func NewMeteringMiddleware(svc interfaces.MeteringService) *MeteringMiddleware {
	m := &MeteringMiddleware{
		meteringSvc: svc,
		buckets:     make(map[string]*meteringBucket),
		stopCh:      make(chan struct{}),
	}
	go m.flushLoop()
	return m
}

func (m *MeteringMiddleware) Stop() {
	close(m.stopCh)
	m.flush()
}

func (m *MeteringMiddleware) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		userID := c.GetString("userID")
		if userID == "" {
			return
		}

		path := c.Request.URL.Path
		for _, skip := range skippedPaths {
			if path == skip {
				return
			}
		}

		method := c.Request.Method
		subtype := "read"
		if method == "POST" || method == "PUT" || method == "DELETE" || method == "PATCH" {
			subtype = "write"
		}

		m.mu.Lock()
		b, ok := m.buckets[userID]
		if !ok {
			b = &meteringBucket{}
			m.buckets[userID] = b
		}
		if subtype == "read" {
			b.read++
		} else {
			b.write++
		}
		m.mu.Unlock()
	}
}

func (m *MeteringMiddleware) flushLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.flush()
		}
	}
}

func (m *MeteringMiddleware) flush() {
	m.mu.Lock()
	snapshot := m.buckets
	m.buckets = make(map[string]*meteringBucket)
	m.mu.Unlock()

	now := time.Now()
	for userID, bucket := range snapshot {
		if bucket.read > 0 {
			m.meteringSvc.Record(types.UsageEvent{
				IdempotencyKey: "apicall:" + userID + ":read:" + now.Format("20060102150405"),
				Owner:          types.BillingOwner{ID: userID, Type: types.OwnerTypeUser},
				ActorID:        userID,
				EventType:      "api_call",
				EventSubtype:   "read",
				Quantity:        bucket.read,
				Source:         "api",
				EventTime:      now,
			})
		}
		if bucket.write > 0 {
			m.meteringSvc.Record(types.UsageEvent{
				IdempotencyKey: "apicall:" + userID + ":write:" + now.Format("20060102150405"),
				Owner:          types.BillingOwner{ID: userID, Type: types.OwnerTypeUser},
				ActorID:        userID,
				EventType:      "api_call",
				EventSubtype:   "write",
				Quantity:        bucket.write,
				Source:         "api",
				EventTime:      now,
			})
		}
	}
}

var skippedPaths = []string{"/health", "/livez", "/readyz", "/metrics"}
