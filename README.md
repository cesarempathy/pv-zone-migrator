# PVC Migrator

A robust Go CLI tool to migrate Kubernetes PersistentVolumeClaims (PVCs) from one AWS Availability Zone to another using AWS EBS Snapshots.

![Demo](https://img.shields.io/badge/TUI-Bubble%20Tea-blue)
![CLI](https://img.shields.io/badge/CLI-Cobra-green)

## Features

- **Beautiful TUI**: Interactive terminal UI with progress bars using Bubble Tea
- **Cobra CLI**: Full-featured command-line interface with flags and help
- **Concurrent Processing**: Processes multiple PVCs in parallel using goroutines with a configurable worker pool
- **Native Libraries**: Uses `k8s.io/client-go` for Kubernetes and `aws-sdk-go-v2` for AWS - no shell command execution
- **Real-time Progress**: Live progress bars for snapshot creation
- **Thread-Safe**: Safe resource cleanup with proper mutex handling
- **Static PV Binding**: Creates new PVs with proper node affinity for the target zone

## Prerequisites

1. **Go 1.21+** installed
2. **kubectl** configured with access to your cluster (uses `~/.kube/config` or `KUBECONFIG` env var)
3. **AWS credentials** configured (via environment variables, `~/.aws/credentials`, or IAM role)
4. **Workloads scaled down**: All pods using the PVCs must be stopped before migration

## Building

```bash
cd pvc-migrator

# Download dependencies
go mod tidy

# Build the binary
go build -o pvc-migrator .
```

## Usage

```bash
# Show help
./pvc-migrator --help
./pvc-migrator migrate --help

# Basic migration with defaults
./pvc-migrator migrate

# Single namespace migration (discovers all PVCs)
./pvc-migrator migrate \
  --namespace budibase \
  --zone eu-west-1a \
  --storage-class gp3

# Multiple namespaces migration (discovers all PVCs in each)
./pvc-migrator migrate \
  --namespace ns1,ns2,ns3 \
  --zone eu-west-1a \
  --storage-class gp3

# Using a specific kubectl context
./pvc-migrator migrate --context my-cluster-context -n budibase

# Using a configuration file (recommended for per-namespace PVC selection)
./pvc-migrator migrate -c config.yaml

# Config file with CLI overrides (CLI flags take precedence)
./pvc-migrator migrate -c config.yaml --dry-run --zone eu-west-1b

# Generate an example configuration file
./pvc-migrator init-config my-config.yaml

# Dry run (shows what would be done)
./pvc-migrator migrate --dry-run
```

### Configuration File

You can use a YAML configuration file instead of (or in combination with) CLI flags. Generate an example with:

```bash
./pvc-migrator init-config config.yaml
```

Example `config.yaml`:

```yaml
# PVC Migrator Configuration
# Each namespace can optionally specify which PVCs to migrate.
# If no PVCs are specified, all PVCs in that namespace will be discovered.

# kubeContext: my-cluster-context  # Optional: kubectl context to use

namespaces:
  - name: namespace-1
    pvcs:
      - pvc-1
      - pvc-2
  - name: namespace-2    # Will discover all PVCs in this namespace

targetZone: eu-west-1a
storageClass: gp3
maxConcurrency: 5
dryRun: false
skipArgoCD: false
argoCDNamespaces:
  - argocd
  - argo-cd
  - gitops
```

**Note:** CLI flags (`--zone`, `--storage-class`, `--context`, etc.) override config file values. The `--namespace` flag from CLI will discover all PVCs (use config file for per-namespace PVC selection).

### Command Line Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--config` | `-c` | | Path to YAML configuration file |
| `--context` | | (current) | Kubernetes context to use |
| `--namespace` | `-n` | `default` | Kubernetes namespace(s), comma-separated (discovers all PVCs) |
| `--zone` | `-z` | `eu-west-1a` | Target AWS Availability Zone |
| `--storage-class` | `-s` | `gp3` | Storage class for new PVs |
| `--concurrency` | | `5` | Max concurrent migrations |
| `--dry-run` | | `false` | Preview without making changes |
| `--skip-argocd` | | `false` | Skip ArgoCD auto-sync handling |
| `--argocd-namespaces` | | `argocd,argo-cd,gitops` | Namespaces to search for ArgoCD apps |

## Terminal UI

The tool provides a beautiful interactive terminal interface:

```
  🚀 PVC Migration Tool

  ╭──────────────────────────────────────╮
  │ Namespaces:      ns1, ns2            │
  │ Target Zone:     eu-west-1a          │
  │ Storage Class:   gp3                 │
  │ Concurrency:     5                   │
  │ PVCs to migrate: 5                   │
  ╰──────────────────────────────────────╯

  Migration Progress:

  database-storage-budibase-couchdb-0    ⣾ Snapshot Progress   ████████░░░░░░ 67%
  database-storage-budibase-couchdb-1    ⣾ Creating Snapshot
  database-storage-budibase-couchdb-2    ○ Pending
  minio-data                             ✓ Completed (2m30s)
  redis-data                             ✗ Failed - get info: PVC not found

  Press q or Ctrl+C to cancel
```

## Migration Process (Per PVC)

1. **Get Info**: Fetches PVC from Kubernetes, retrieves PV name and AWS Volume ID
2. **Snapshot**: Creates an AWS EBS snapshot of the original volume
3. **Wait for Snapshot**: Shows real-time progress until snapshot completes
4. **Create Volume**: Creates a new EBS volume from the snapshot in the target AZ
5. **Wait for Volume**: Ensures the new volume is available
6. **Cleanup**: Removes finalizers and deletes old PVC and PV
7. **Create Static PV**: Creates a new PV pointing to the new volume with proper node affinity
8. **Create Bound PVC**: Creates a new PVC that binds to the static PV

## AWS Permissions Required

The IAM user/role needs the following EC2 permissions:

```json
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Action": [
                "ec2:CreateSnapshot",
                "ec2:DescribeSnapshots",
                "ec2:CreateVolume",
                "ec2:DescribeVolumes",
                "ec2:CreateTags"
            ],
            "Resource": "*"
        }
    ]
}
```

## Kubernetes Permissions Required

The kubeconfig user needs permissions to:

- Get, Update, Delete PersistentVolumeClaims in the target namespace
- Get, Update, Delete, Create PersistentVolumes (cluster-scoped)

## Post-Migration

After successful migration:

1. Verify PVCs are bound: `kubectl get pvc -n budibase`
2. Scale up your workloads
3. Verify pods are scheduled in the target zone
4. Consider deleting old snapshots/volumes from AWS to save costs

## Troubleshooting

**PVC not bound after migration:**
- Check if the PV was created: `kubectl get pv | grep static`
- Verify storage class exists: `kubectl get storageclass gp3`

**AWS API rate limiting:**
- Reduce `--concurrency` value

**Permission denied errors:**
- Verify AWS credentials have required permissions
- Verify kubeconfig has required RBAC permissions
