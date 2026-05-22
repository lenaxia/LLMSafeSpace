package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseWatchNamespaces_EmptyMeansAll(t *testing.T) {
	assert.Nil(t, parseWatchNamespaces(""))
	assert.Nil(t, parseWatchNamespaces("   "))
}

func TestParseWatchNamespaces_StarMeansAll(t *testing.T) {
	assert.Nil(t, parseWatchNamespaces("*"))
	assert.Nil(t, parseWatchNamespaces(" * "))
}

func TestParseWatchNamespaces_SingleNamespace(t *testing.T) {
	got := parseWatchNamespaces("llmsafespace")
	assert.Len(t, got, 1)
	_, ok := got["llmsafespace"]
	assert.True(t, ok)
}

func TestParseWatchNamespaces_MultipleNamespaces(t *testing.T) {
	got := parseWatchNamespaces("ns1,ns2,ns3")
	assert.Len(t, got, 3)
	for _, ns := range []string{"ns1", "ns2", "ns3"} {
		_, ok := got[ns]
		assert.Truef(t, ok, "expected namespace %s present", ns)
	}
}

func TestParseWatchNamespaces_TrimsWhitespace(t *testing.T) {
	got := parseWatchNamespaces(" ns1 , ns2 ,   ns3   ")
	assert.Len(t, got, 3)
	for _, ns := range []string{"ns1", "ns2", "ns3"} {
		_, ok := got[ns]
		assert.Truef(t, ok, "expected namespace %s present", ns)
	}
}

func TestParseWatchNamespaces_IgnoresEmptyEntries(t *testing.T) {
	got := parseWatchNamespaces("ns1,,ns2,")
	assert.Len(t, got, 2)
	_, ok := got["ns1"]
	assert.True(t, ok)
	_, ok = got["ns2"]
	assert.True(t, ok)
}

func TestParseWatchNamespaces_AllEmptyEntriesReturnsNil(t *testing.T) {
	assert.Nil(t, parseWatchNamespaces(",,,"))
	assert.Nil(t, parseWatchNamespaces(" , , "))
}
