# Terraform Deployment Examples

This document provides detailed examples for deploying the EdgeCenter ExternalDNS Webhook and ExternalDNS itself using Terraform.

## Prerequisites

1.  Ensure you have Terraform and the Kubernetes provider configured.
2.  Create a Kubernetes secret for the EdgeCenter API key (named `token` in this example). Choose one method:

    **Method 1: Regular Kubernetes Secret**

    ```bash
    kubectl create secret generic edgecenter-webhook-secret \
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
              "secretKey" = "token" # Key in the resulting K8s secret
              "remoteRef" = {
                "key"      = "external-dns/edgecenter" # Path to the secret in your vault
                "property" = "token"                   # Name of the field in the vault secret
              }
            }
          ]
        }
      }
    }
    ```

## Example Terraform Configuration

```terraform
variable "namespace" {
  description = "Namespace for deployment"
  default     = "external-dns"
}

variable "webhook_image" {
  description = "Docker image for the webhook (use specific tag, not latest!)"
  default     = "cr.yandex/crpminendqjcho56q23n/external-dns-edgecenter-webhook:latest" # Specify your image!
}

variable "dry_run" {
  description = "Enable dry-run mode"
  type        = bool
  default     = false
}

variable "annotation_filter" {
  description = "Annotation filter for ExternalDNS (for multi-provider setups)"
  default     = "" # No filter by default
}

variable "webhook_replicas" {
  description = "Number of webhook replicas"
  default     = 1
}

provider "kubernetes" {
  # Your Kubernetes provider configuration
}

# --- Webhook Deployment ---
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
            container_port = 8888 # Webhook listens on port 8888
            name           = "http"
          }

          env {
            name  = "DRY_RUN"
            value = tostring(var.dry_run) # Ensure value is a string
          }
          env {
            name = "EDGECENTER_API_KEY"
            value_from {
              secret_key_ref {
                # Secret created manually or via ExternalSecret
                name = "edgecenter-webhook-secret"
                key  = "token" # Matches the secretKey defined above
              }
            }
          }
          # Optional: Set custom EdgeCenter API URL
          # env {
          #   name = "EDGECENTER_BASE_URL"
          #   value = "https://api.example.com/dns"
          # }

          liveness_probe {
            http_get {
              path = "/health"
              port = "http" # Refers to the port named 'http' above (8888)
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
              cpu    = "50m" # Adjusted based on manifest
              memory = "64Mi"
            }
            limits = {
              cpu    = "100m"
              memory = "128Mi"
            }
          }

          # Optional Security Context (adjust user ID if needed)
          security_context {
            read_only_root_filesystem = true
            run_as_non_root           = true
            run_as_user               = 1001 # Use a non-zero user ID
            capabilities {
              drop = ["ALL"]
            }
          }
        }
      }
    }
  }
}

# --- Webhook Service ---
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
      port        = 8888 # Port that ExternalDNS talks to
      target_port = "http" # Name of the port (8888) in the Deployment container
      protocol    = "TCP"
      name        = "http"
    }
    type = "ClusterIP"
  }
}

# --- ExternalDNS Deployment (using the webhook) ---
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
        # Assumes RBAC is set up (see multi-provider section for example RBAC)
        service_account_name = "external-dns" # Use the created SA
        container {
          name  = "external-dns"
          image = "registry.k8s.io/external-dns/external-dns:v0.13.4" # Use desired version
          args = [
            "--source=service",
            "--source=ingress",
            "--provider=webhook",
            "--webhook-provider-url=http://external-dns-edgecenter-webhook:8888", # Address of our webhook service (adjust if namespace differs)
            # Use annotation filter ONLY if running multiple ExternalDNS instances
            # "--annotation-filter=${var.annotation_filter}",
            "--registry=txt",
            "--txt-owner-id=external-dns-edgecenter", # Unique owner ID
            "--request-timeout=2m", # Increased timeout for API requests
            # "--domain-filter=example.com", # Optional: filter domains managed by this instance
            # "--policy=sync", # Optional: 'sync' or 'upsert-only'
            # "--log-level=info" # Optional: set log level (debug is useful)
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

# (Example for another provider like Yandex is omitted for brevity but available in previous revisions of the main README)
```

## Applying the Configuration

```bash
terraform init
terraform plan
terraform apply
```

*(For RBAC setup and multi-provider configurations using Terraform, please refer back to the main README.md)* 