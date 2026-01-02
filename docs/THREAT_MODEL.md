# STRIDE Threat Model Analysis

**Date:** January 2, 2026
**Target:** `pvc-migrator` CLI Tool
**Context:** AWS EBS & Amazon EKS Infrastructure Migration

## System Context

*   **Tool Type:** CLI Utility (Golang)
*   **Core Function:** Automates the backup, snapshot, and restore process of EBS volumes to move them between AWS Availability Zones.
*   **Privileges:** Requires elevated AWS IAM permissions (EC2/EBS full access) and Kubernetes RBAC (PV/PVC management).
*   **Data Flow:**
    1.  Read Kubeconfig & CLI Args.
    2.  Query K8s for PVC/PV details.
    3.  Scale down K8s workloads.
    4.  Call AWS API to Snapshot & Create Volume in new AZ.
    5.  Create new PV/PVC in K8s.
    6.  Cleanup old resources.

## Threat Matrix

| Component | STRIDE Category | Threat Scenario | Impact | Risk Level | Mitigation Status |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Migration Logic**<br>`internal/migrator` | **Denial of Service**<br>(Data Loss) | **Race Condition during Cleanup:** The tool previously deleted the old PVC/PV *before* creating the new Static PV/PVC. A crash during this window would leave the volume "orphaned" (detached from K8s) and the application down. | **High** | **Critical** | **Fixed**<br>Refactored workflow to Create New PV/PVC *before* cleanup. |
| **K8s Client**<br>`internal/k8s` | **Elevation of Privilege** | **Unchecked Context:** The tool loads kubeconfig without validating the context. A user might accidentally run this against a production cluster (`prod`) instead of a development one (`dev`), leading to unintended data operations. | **Critical** | **High** | **Partially Fixed**<br>Added warning for "prod" contexts. Future: Implement `SelfSubjectAccessReview`. |
| **AWS Client**<br>`internal/aws` | **Tampering** | **Tag Injection:** User-provided inputs (Namespace, PVC Name) are directly injected into AWS Resource Tags. Malicious input could disrupt cost allocation or automation scripts relying on these tags. | **Medium** | **Medium** | **Open**<br>Needs `SanitizeTag()` implementation. |
| **CLI Config**<br>`cmd/migrate.go` | **Tampering** | **Supply Chain/Input:** The `Validate()` method performs only basic checks. It does not validate that the `TargetZone` is a valid AWS AZ, potentially causing failures late in the migration process. | **Low** | **Low** | **Open**<br>Needs strict AZ validation against AWS API. |
| **Logging**<br>`cmd/migrate.go` | **Information Disclosure** | **Metadata Leak:** The tool logs Snapshot IDs and Volume IDs to stdout. While not secrets, this leaks infrastructure topology which could be useful for an attacker mapping the environment. | **Low** | **Low** | **Open**<br>Recommend structured logging (slog) with log levels. |

## Detailed Findings & Mitigations

### 1. Data Loss (Race Condition)
*   **Issue:** The original workflow deleted the source PVC before the destination PVC was fully bound.
*   **Fix:** The workflow in `internal/migrator/migrator.go` has been reordered.
    *   *Old:* Scale Down -> Snapshot -> **Delete Old** -> Create New.
    *   *New:* Scale Down -> Snapshot -> **Create New** -> Delete Old.
*   **Result:** If the tool crashes, the new volume exists and is bound, or the old volume remains untouched. The "orphaned volume" state is minimized.

### 2. Privilege Escalation (Context Confusion)
*   **Issue:** No validation of the Kubernetes context.
*   **Fix:** `internal/k8s/client.go` now inspects the current context name.
*   **Result:** If the context name contains "prod" (case-insensitive), a warning is printed to stdout alerting the operator.

### 3. Input Validation (Tampering)
*   **Recommendation:** Implement strict regex validation for all user inputs that are passed to external APIs (AWS Tags, K8s Object Names).
*   **Pattern:** `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` (DNS-1123 subdomain).

## Future Security Roadmap
1.  **Atomic State Management:** Implement a local "Write-Ahead Log" (WAL) to track migration steps on disk, allowing the tool to resume from a crash state automatically.
2.  **Least Privilege Policy:** Publish a minimal IAM Policy JSON and K8s Role YAML required for the tool to function, rather than asking for "Full Access".
3.  **Signed Binaries:** Ensure release binaries are signed (e.g., using Cosign) to prevent supply chain attacks.
