# Sandbox Controller Overview for LLMSafeSpace

## Overview

The Sandbox Controller is a critical component of the LLMSafeSpace platform, responsible for managing the lifecycle of both sandbox environments and warm pools. This document provides a detailed design for the controller, including Custom Resource Definitions (CRDs), reconciliation loops, and resource lifecycle management.

The controller manages both sandbox environments and pools of pre-initialized sandbox environments (warm pools) for faster startup times, providing a unified approach to resource management.
