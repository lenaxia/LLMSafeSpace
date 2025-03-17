# LLMSafeSpace Core Packages

This document provides technical documentation for the core Go packages that power the LLMSafeSpace platform. All packages follow Go 1.19+ conventions and Kubernetes operator patterns.

## Table of Contents

1. [Kubernetes Client](#kubernetes-client)
2. [CRD Definitions](#crd-definitions)
3. [Utilities](#utilities)
4. [Logging](#logging)
5. [HTTP Utilities](#http-utilities)
6. [Configuration](#configuration)
7. [Interfaces](#interfaces)

---

## Kubernetes Client

**Package:** `kubernetes`  
**Key Components:**
- Client management with leader election
- Custom resource informers
- Sandbox operations executor
- Warm pool/pod controller

**Key Features:**
