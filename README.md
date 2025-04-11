# EdgeCenter DNS Webhook for ExternalDNS

This project provides a webhook provider implementation for [ExternalDNS](https://github.com/kubernetes-sigs/external-dns), allowing DNS record management in EdgeCenter DNS.

## Features

- Full support for the ExternalDNS Webhook API
- Manage DNS records in EdgeCenter DNS
- Support for all DNS record types
- Dry-run mode for safe debugging of DNS changes
- Annotation filtering for working with multiple ExternalDNS instances
- Integration with External Secrets Operator for secure secret management (optional)

## Requirements

- Go 1.19 or higher (for building)
- Docker (for building the image)
- Kubernetes 1.16 or higher
- ExternalDNS v0.13.0 or higher
- Access to the EdgeCenter DNS API
- Terraform v1.0 or higher (for deployment)
- Kubectl

## Building the Docker Image

```bash
# Clone the repository
git clone https://viory.gitlab.yandexcloud.net/viory/external-dns-edgecenter-webhook.git
cd external-dns-edgecenter-webhook

# Build the Docker image
# Replace <your-registry> and <tag> with your values
docker build -t <your-registry>/external-dns-edgecenter-webhook:<tag> .

# Push the image to your Docker repository
docker push <your-registry>/external-dns-edgecenter-webhook:<tag>
```

## Deployment with Terraform

### Prerequisites

1.  Ensure you have Terraform and the Kubernetes provider configured.
2.  Create a Kubernetes secret for the EdgeCenter token.

    **Method 1: Regular Kubernetes Secret**

    ```bash
    kubectl create secret generic edgecenter-credentials \
      --from-literal=token='YOUR_EDGECENTER_API_TOKEN' \
      --namespace external-dns # Specify your namespace
    ```

    **Method 2: Integration with External Secrets Operator**

    If you use the External Secrets Operator, configure an `ExternalSecret`:

    ```terraform
    resource "kubernetes_manifest" "external_secret_edgecenter" {
      manifest = {
        "apiVersion" = "external-secrets.io/v1beta1"
        "kind"       = "ExternalSecret"
        "metadata" = {
          "name"      = "edgecenter-webhook-secret"
          "namespace" = var.namespace # Your namespace
        }
        "spec" = {
          "refreshInterval" = "1h"
          "secretStoreRef" = {
            "name" = "vault-backend" # Your SecretStore name
            "kind" = "ClusterSecretStore"
          }
          "target" = {
            "name" = "edgecenter-webhook-secret" # Name of the Kubernetes secret to be created
          }
          "data" = [
            {
              "secretKey" = "token"
              "remoteRef" = {
                "key"      = "external-dns/edgecenter" # Path to the secret in your vault
                "property" = "token"                   # Name of the field in the secret
              }
            }
          ]
        }
      }
    }
    ```

### Example Terraform Configuration

```terraform
variable "namespace" {
  description = "Namespace for deployment"
  default     = "external-dns"
}

variable "webhook_image" {
  description = "Docker image for the webhook"
  default     = "cr.yandex/crpminendqjcho56q23n/external-dns-edgecenter-webhook:latest" # Specify your image!
}

variable "dry_run" {
  description = "Enable dry-run mode"
  type        = bool
  default     = false
}

variable "annotation_filter" {
  description = "Annotation filter for ExternalDNS"
  default     = "external-dns.alpha.kubernetes.io/target-provider=edgecenter"
}

variable "webhook_replicas" {
  description = "Number of webhook replicas"
  default     = 1
}

provider "kubernetes" {
  # Your Kubernetes provider configuration
}

resource "kubernetes_deployment" "webhook" {
  metadata {
    name      = "external-dns-edgecenter-webhook"
    namespace = var.namespace
    labels = {
      app = "external-dns-edgecenter-webhook"
    }
  }

  spec {
    replicas = var.webhook_replicas

    selector {
      match_labels = {
        app = "external-dns-edgecenter-webhook"
      }
    }

    template {
      metadata {
        labels = {
          app = "external-dns-edgecenter-webhook"
        }
      }

      spec {
        service_account_name = "default" # Specify your Service Account if needed

        container {
          name  = "webhook"
          image = var.webhook_image
          image_pull_policy = "IfNotPresent"

          port {
            container_port = 8080
            name           = "http"
          }

          env {
            name  = "DRY_RUN"
            value = var.dry_run
          }
          env {
            name = "EDGECENTER_API_KEY"
            value_from {
              secret_key_ref {
                # Specify the secret name created manually or via ExternalSecret
                name = "edgecenter-webhook-secret" # or "edgecenter-credentials"
                key  = "token"
              }
            }
          }

          liveness_probe {
            http_get {
              path = "/health"
              port = "http"
            }
            initial_delay_seconds = 10
            period_seconds        = 5
          }

          readiness_probe {
            http_get {
              path = "/health"
              port = "http"
            }
            initial_delay_seconds = 5
            period_seconds        = 5
          }

          resources {
            requests = {
              cpu    = "10m"
              memory = "64Mi"
            }
            limits = {
              cpu    = "100m"
              memory = "128Mi"
            }
          }

          security_context {
            read_only_root_filesystem = true
            run_as_non_root           = true
            run_as_user               = 1000
            capabilities {
              drop = ["ALL"]
            }
          }
        }
      }
    }
  }
}

resource "kubernetes_service" "webhook" {
  metadata {
    name      = "external-dns-edgecenter-webhook"
    namespace = var.namespace
    labels = {
      app = "external-dns-edgecenter-webhook"
    }
  }
  spec {
    selector = {
      app = "external-dns-edgecenter-webhook"
    }
    port {
      port        = 8888 # Port that ExternalDNS listens on
      target_port = "http" # Name of the port in the Deployment
      protocol    = "TCP"
      name        = "http"
    }
    type = "ClusterIP"
  }
}

# Example Deployment for ExternalDNS targeting EdgeCenter via Webhook
resource "kubernetes_deployment" "external_dns_edgecenter" {
  metadata {
    name      = "external-dns-edgecenter"
    namespace = var.namespace
    labels = {
      app = "external-dns-edgecenter"
    }
  }
  spec {
    replicas = 1 # Can be made a variable
    selector {
      match_labels = {
        app = "external-dns-edgecenter"
      }
    }
    template {
      metadata {
        labels = {
          app = "external-dns-edgecenter"
        }
      }
      spec {
        # Assumes RBAC is set up (see multi-provider section for example)
        service_account_name = "external-dns" # Use the created SA
        container {
          name  = "external-dns"
          image = "registry.k8s.io/external-dns/external-dns:v0.13.0" # Use desired version
          args = [
            "--source=service",
            "--source=ingress",
            "--provider=webhook",
            "--webhook-provider-url=http://external-dns-edgecenter-webhook:8888", # Address of our webhook service
            "--annotation-filter=${var.annotation_filter}",
            "--registry=txt",
            "--txt-owner-id=external-dns-edgecenter", # Unique owner ID
            "--request-timeout=2m", # Increased timeout for API requests
            # "--domain-filter=example.com", # Optional: filter domains managed by this instance
            # "--policy=sync", # Optional: 'sync' or 'upsert-only'
            # "--log-level=info" # Optional: set log level
          ]
          # Add resources and probes as needed
          resources {
             requests = {
               cpu    = "10m"
               memory = "64Mi"
             }
             limits = {
               cpu    = "100m"
               memory = "128Mi"
             }
          }
        }
      }
    }
  }
}

# Example Deployment for ExternalDNS targeting another provider (e.g., Yandex)
# See Multi-Provider section for RBAC setup if needed
# resource "kubernetes_deployment" "external_dns_yandex" {
#   metadata {
#     name      = "external-dns-yandex"
#     namespace = var.namespace
#     labels = {
#       app = "external-dns-yandex"
#     }
#   }
#   spec {
#     replicas = 1 # Can be made a variable
#     selector {
#       match_labels = {
#         app = "external-dns-yandex"
#       }
#     }
#     template {
#       metadata {
#         labels = {
#           app = "external-dns-yandex"
#         }
#       }
#       spec {
#         service_account_name = "external-dns" # Use the created SA
#         container {
#           name  = "external-dns"
#           image = "registry.k8s.io/external-dns/external-dns:v0.13.0"
#           args = [
#             "--source=service",
#             "--source=ingress",
#             "--provider=yandex", # Change to your other provider
#             "--registry=txt",
#             "--txt-owner-id=external-dns-yandex", # Different owner ID
#             # Add other provider-specific args and env vars
#             # Remove --annotation-filter or use a different one if needed
#             "--request-timeout=2m",
#             # "--domain-filter=example.ru" # Optional
#           ]
#           # env {
#           #   name = "YANDEX_CLOUD_FOLDER_ID"
#           #   value_from {
#           #     secret_key_ref {
#           #       name = "yandex-dns-credentials" # Secret for Yandex provider
#           #       key  = "folder-id"
#           #     }
#           #   }
#           # }
#           # env {
#           #   name = "YANDEX_CLOUD_TOKEN"
#           #   value_from {
#           #     secret_key_ref {
#           #       name = "yandex-dns-credentials"
#           #       key  = "token"
#           #     }
#           #   }
#           # }
#           # Add resources and probes as needed
#         }
#       }
#     }
#   }
# }
```

### Applying the Configuration

```bash
terraform init
terraform plan
terraform apply
```

## Configuration

Key parameters are managed via environment variables in the webhook's Deployment manifest:

| Environment Variable | Terraform Variable | Description                                       | Default Value                                            |
|----------------------|--------------------|---------------------------------------------------|----------------------------------------------------------|
| `DRY_RUN`            | `dry_run`          | Enable dry-run mode (no changes made to DNS)      | `false`                                                  |
| `EDGECENTER_API_KEY` | (from secret)      | EdgeCenter API access token                       | -                                                        |
| `PORT`               | (in code)          | Port the webhook server listens on                | `8080`                                                   |
| `LOG_LEVEL`          | (in code)          | Logging level (debug, info, warn, error)          | `info`                                                   |

## Using with Multiple DNS Providers

### General Description

If your cluster already runs ExternalDNS with another provider (e.g., Yandex DNS), you can configure both providers to work simultaneously. To do this, deploy two instances of ExternalDNS:

1.  **ExternalDNS for EdgeCenter:** Configured to use the webhook provider and filters resources based on the `external-dns.alpha.kubernetes.io/target-provider=edgecenter` annotation.
2.  **ExternalDNS for Other Provider (e.g., Yandex):** Configured to use its specific provider (e.g., `yandex`) and typically *does not* use the `annotation-filter` (or uses a different one), processing all other resources.

### Setting up RBAC for ExternalDNS (Terraform Example)

ExternalDNS requires permissions to read Services, Ingresses, Endpoints, Pods, and Nodes in the cluster. Create the necessary RBAC resources (usually only needed once per cluster, shared by both ExternalDNS instances):

```terraform
resource "kubernetes_service_account" "external_dns" {
  metadata {
    name      = "external-dns"
    namespace = var.namespace
    # You might add annotations for IAM roles (e.g., for AWS IRSA or GCP Workload Identity)
    # annotations = {
    #   "eks.amazonaws.com/role-arn" = "arn:aws:iam::ACCOUNT_ID:role/external-dns"
    # }
  }
}

resource "kubernetes_cluster_role" "external_dns" {
  metadata {
    name = "external-dns"
  }

  rule {
    api_groups = [""]
    resources  = ["services", "endpoints", "pods", "nodes"] # Remove "nodes" if using --no-nodes
    verbs      = ["get", "watch", "list"]
  }

  rule {
    api_groups = ["extensions", "networking.k8s.io"]
    resources  = ["ingresses"]
    verbs      = ["get", "watch", "list"]
  }
  # Add rules for other sources if needed (e.g., "gateway.networking.k8s.io" for Gateway API)
}

resource "kubernetes_cluster_role_binding" "external_dns" {
  metadata {
    name = "external-dns"
  }
  role_ref {
    api_group = "rbac.authorization.k8s.io"
    kind      = "ClusterRole"
    name      = kubernetes_cluster_role.external_dns.metadata.0.name
  }
  subject {
    kind      = "ServiceAccount"
    name      = kubernetes_service_account.external_dns.metadata.0.name
    namespace = var.namespace
  }
}
```

### Configuring ExternalDNS for EdgeCenter (via Terraform)

Use the `kubernetes_deployment.external_dns_edgecenter` resource shown in the [Example Terraform Configuration](#example-terraform-configuration) section above. Ensure it uses the correct `service_account_name` and sets the `--annotation-filter`.

### Configuring ExternalDNS for Another Provider (e.g., Yandex) (via Terraform)

First, create the secret containing credentials for your other provider (e.g., `yandex-dns-credentials`). Then, create a separate Deployment (like the commented-out `kubernetes_deployment.external_dns_yandex` example above):
*   Adjust the `metadata.name` and `labels`.
*   Set the `--provider` flag correctly (e.g., `--provider=yandex`).
*   Use a different `--txt-owner-id` (e.g., `external-dns-yandex`).
*   Include any required environment variables for that provider (e.g., `YANDEX_CLOUD_FOLDER_ID`, `YANDEX_CLOUD_TOKEN`).
*   **Crucially:** Either remove the `--annotation-filter` argument entirely (so it handles resources *without* the EdgeCenter annotation) or use a different, specific annotation if needed.

### Usage

After setup, you can control which DNS provider manages a record using annotations on your Services and Ingresses:

```yaml
# Example 1: Record in EdgeCenter DNS
apiVersion: v1
kind: Service
metadata:
  name: app-edgecenter
  annotations:
    external-dns.alpha.kubernetes.io/hostname: app.example.com
    external-dns.alpha.kubernetes.io/target-provider: edgecenter  # Specifies the EdgeCenter provider
spec:
  type: LoadBalancer
  ports:
  - port: 80
  selector:
    app: my-app

---
# Example 2: Record in Yandex DNS (or other default provider)
apiVersion: v1
kind: Service
metadata:
  name: app-yandex
  annotations:
    external-dns.alpha.kubernetes.io/hostname: app.example.ru
    # No target-provider annotation, record handled by the Yandex DNS instance
spec:
  type: LoadBalancer
  ports:
  - port: 80
  selector:
    app: my-app

# ... (Ingress examples are similar) ...
```

### Example YAML Manifests for Testing

#### 1. Nginx Test App with Service and Two Ingresses (EdgeCenter & Yandex)

```yaml
---
# Deployment for the test app
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-test
  namespace: external-dns # Adjust namespace if needed
spec:
  selector:
    matchLabels:
      app: nginx-test
  replicas: 1
  template:
    metadata:
      labels:
        app: nginx-test
    spec:
      containers:
      - name: nginx
        image: nginx:stable
        ports:
        - containerPort: 80
          name: http
        resources:
          requests:
            cpu: 10m
            memory: 20Mi
          limits:
            cpu: 100m
            memory: 100Mi

---
# Service for the test app
apiVersion: v1
kind: Service
metadata:
  name: nginx-test
  namespace: external-dns # Adjust namespace if needed
spec:
  ports:
  - port: 80
    targetPort: http
    protocol: TCP
    name: http
  selector:
    app: nginx-test

---
# Ingress for EdgeCenter DNS
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: nginx-test-edgecenter
  namespace: external-dns # Adjust namespace if needed
  annotations:
    external-dns.alpha.kubernetes.io/target-provider: edgecenter
    kubernetes.io/ingress.class: nginx # Or your ingress controller class
    # Optional annotations for NGINX Ingress Controller
    # nginx.ingress.kubernetes.io/ssl-redirect: "false"
    # nginx.ingress.kubernetes.io/use-regex: "true"
spec:
  rules:
  - host: nginx-edge.your-edgecenter-domain.com  # Replace with your domain
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: nginx-test
            port:
              name: http

---
# Ingress for Yandex DNS (without target-provider annotation)
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: nginx-test-yandex
  namespace: external-dns # Adjust namespace if needed
  annotations:
    kubernetes.io/ingress.class: nginx # Or your ingress controller class
    # Optional annotations for NGINX Ingress Controller
    # nginx.ingress.kubernetes.io/ssl-redirect: "false"
    # nginx.ingress.kubernetes.io/use-regex: "true"
spec:
  rules:
  - host: nginx-yandex.your-yandex-domain.ru  # Replace with your domain
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: nginx-test
            port:
              name: http
```

#### 2. Test App with TLS (HTTPS)

```yaml
---
# Deployment and Service are the same as the previous example
# Secret with TLS certificate (replace with your data)
apiVersion: v1
kind: Secret
metadata:
  name: tls-secret-test
  namespace: external-dns # Adjust namespace if needed
type: kubernetes.io/tls
data:
  # Replace with your base64 encoded certificate and key
  tls.crt: LS0tLS1CRUdJTi...
  tls.key: LS0tLS1CRUdJTi...

---
# Ingress with TLS for EdgeCenter DNS
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: tls-test-edgecenter
  namespace: external-dns # Adjust namespace if needed
  annotations:
    external-dns.alpha.kubernetes.io/target-provider: edgecenter
    kubernetes.io/ingress.class: nginx # Or your ingress controller class
spec:
  tls:
  - hosts:
    - secure-edge.your-edgecenter-domain.com # Replace with your domain
    secretName: tls-secret-test
  rules:
  - host: secure-edge.your-edgecenter-domain.com # Replace with your domain
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: nginx-test
            port:
              name: http
```

#### 3. Commands for Applying and Checking

```bash
# Apply the manifests
kubectl apply -f test-manifests.yaml

# Check created resources
kubectl get deploy,svc,ing -n external-dns # Adjust namespace if needed

# Check EdgeCenter ExternalDNS logs
kubectl logs -f deployment/external-dns-edgecenter -n external-dns # Adjust names/namespace

# Check Yandex ExternalDNS logs (if deployed)
# kubectl logs -f deployment/external-dns-yandex -n external-dns # Adjust names/namespace

# Check EdgeCenter Webhook logs
kubectl logs -f deployment/external-dns-edgecenter-webhook -n external-dns # Adjust names/namespace
```

After creating the resources, ExternalDNS should detect the new Ingresses and create the corresponding DNS records in EdgeCenter DNS and Yandex DNS.

### How it Works

1.  **EdgeCenter ExternalDNS:**
    *   Processes only resources with the `external-dns.alpha.kubernetes.io/target-provider=edgecenter` annotation.
    *   Sends requests to the `external-dns-edgecenter-webhook`.
2.  **Yandex ExternalDNS:**
    *   Processes all other resources (without that specific annotation).
    *   Directly interacts with the Yandex Cloud API.
3.  **EdgeCenter Webhook:**
    *   Receives requests from the EdgeCenter ExternalDNS instance.
    *   Interacts with the EdgeCenter DNS API to create/update/delete records (or logs actions in `dryRun` mode).

### Recommendations

1.  Use different `txt-owner-id` values for each ExternalDNS instance.
2.  Check the logs of both ExternalDNS instances and the webhook when debugging.
3.  Use `dryRun=true` for initial deployment testing.
4.  Ensure annotations on your resources are correct.
5.  Consider using different domains for different providers if applicable.
6.  Verify that you have configured the correct permissions (e.g., IAM roles) for both providers.
7.  Using separate namespaces for different providers might simplify management, if feasible.

### Known Issues

1.  When using multiple providers with `registry=txt`, ensure there are no conflicts in TXT records (use distinct `txt-owner-id`).
2.  If using different domains per provider, ensure any `--domain-filter` arguments in ExternalDNS are set correctly.
3.  Switching the provider for an existing record might require manual deletion of the old record.
4.  DNS record updates might be delayed due to DNS caching and TTLs.
5.  In `dryRun=true` mode, records won't be created, but logs will show the changes that would have been applied.

## API

The webhook implements the standard [ExternalDNS Webhook Provider API](https://github.com/kubernetes-sigs/external-dns/blob/master/docs/proposal/webhook.md).

## Limitations

### Subzone Record Management

When managing records in subzones (e.g., `sub.example.com`) using this webhook provider with ExternalDNS, consider the following:

1.  **TXT Records Requirement:** ExternalDNS defaults to `registry=txt`. This means for each managed record (e.g., an A record for `sub.example.com`), it creates an associated TXT record for metadata storage (e.g., `a-sub.example.com`).
2.  **TXT Record Placement:** Understand where ExternalDNS expects to find or create these TXT records:
    *   **TXT for Regular Records:** The TXT record for a regular record (e.g., `a-sub.test.example.com` for `sub.test.example.com`) must reside **in the same zone** as the managed record (`test.example.com`).
    *   **TXT for Zones Themselves:** The TXT record managing the zone itself (e.g., `a-test.example.com` for the zone `test.example.com`) must reside **in the parent zone** (`example.com`).
3.  **API Permission Requirements:**
    *   To manage records within the `test.example.com` zone (including `sub.test.example.com` and `a-sub.test.example.com`), your EdgeCenter API token needs permissions for **that specific zone** (`test.example.com`).
    *   To manage the `test.example.com` zone itself (via the `a-test.example.com` TXT record), your EdgeCenter API token needs permissions for the **parent zone** (`example.com`).

**Consequence:** If you are managing the `test.example.com` zone, but your API token **lacks permissions** for the parent zone `example.com`, ExternalDNS **will fail** to create the necessary `a-test.example.com` TXT record in the `example.com` zone. This might prevent ExternalDNS from correctly managing records in `test.example.com` or cause it to repeatedly attempt creating the missing TXT record.

**Recommendations:**

*   **Use a Single API Token with Broad Permissions:** This is the most reliable approach. Grant the API token permissions for all parent zones required for creating the ownership TXT records.
*   **Consider `--txt-prefix` (with caution):** If managing parent zones isn't possible, you could try the `--txt-prefix` flag for ExternalDNS. This changes the TXT record names (e.g., `prefix-a-test.example.com`) and causes them to be created **in the same zone** (`test.example.com`) for which you have permissions, instead of the parent. **However,** if the EdgeCenter API expects the **exact TXT record name** (e.g., `a-test.example.com`) to manage the `test.example.com` zone, creating a record with a modified name like `prefix-a-test.example.com` (even with permissions for `test.example.com`) might not work or might not achieve the intended zone management. Use this flag cautiously and test thoroughly. *(The current webhook implementation does not have special handling for custom prefixes)*.
*   **Explore Other Registries:** In some cases, using a different ExternalDNS registry type (e.g., `crd`) might be more suitable if it aligns better with your access permission constraints and avoids the need for TXT records in parent zones.

## Local Development

```bash
# Install dependencies
go mod download

# Run tests
go test ./...

# Run the webhook locally (port 8080)
# Ensure the EDGECENTER_API_KEY environment variable is set
export EDGECENTER_API_KEY="your-api-key"
go run ./cmd/webhook/main.go
``` 