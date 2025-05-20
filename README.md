# EdgeCenter DNS Webhook for ExternalDNS

This project provides a webhook provider implementation for [ExternalDNS](https://github.com/kubernetes-sigs/external-dns), allowing DNS record management in EdgeCenter DNS.

## Features

- Full support for the ExternalDNS Webhook API
- Manage DNS records in EdgeCenter DNS via its API
- Supports common DNS record types (A, AAAA, CNAME, TXT, etc.)
- Dry-run mode for testing DNS changes without applying them
- Annotation filtering for using multiple ExternalDNS instances
- Integration with External Secrets Operator for secure secret management (optional)

## Requirements

- Go 1.24 or higher (for building)
- Docker (for building the image)
- Kubernetes 1.16 or higher
- ExternalDNS v0.13.0 or higher
- Access to the EdgeCenter DNS API and an API Key
- `kubectl`
- Terraform v1.0 or higher (optional, for Terraform deployment)

## Installation

You can either use a pre-built container image (if available) or build your own.

### Using Pre-built Image

*(TODO: Add instructions here if pre-built images are published, e.g., on Docker Hub or another registry)*

### Building the Docker Image

```bash
# Clone the repository
# Replace with the actual repository URL
git clone https://<your-repo-url>/external-dns-edgecenter-webhook.git
cd external-dns-edgecenter-webhook

# Build the Docker image
# Replace <your-registry> and <tag> with your values
docker build -t <your-registry>/external-dns-edgecenter-webhook:<tag> .

# Push the image to your Docker repository
docker push <your-registry>/external-dns-edgecenter-webhook:<tag>
```

## Deployment

You can deploy the webhook using the provided Kubernetes manifest or using Terraform.

### Using Kubernetes Manifest

1.  **Create a Secret:** Store your EdgeCenter API key in a Kubernetes secret. Ensure the key in the secret is named `api-key`.

    ```bash
    kubectl create secret generic edgecenter-credentials \
      --from-literal=api-key='YOUR_EDGECENTER_API_KEY' \
      --namespace external-dns # Adjust namespace if needed
    ```

2.  **Deploy the Webhook:** Apply the manifest located in `deploy/webhook.yaml`.

    ```bash
    kubectl apply -f deploy/webhook.yaml
    ```

    This manifest creates:
    *   A `Deployment` to run the webhook pod(s).
    *   A `Service` (`external-dns-edgecenter-webhook`) of type `ClusterIP` exposing the webhook internally on port `8888`.
    *   The `Secret` reference assumes the secret is named `edgecenter-credentials` and contains the key `api-key`.

    **Notes:**
    *   The default manifest uses the image name `external-dns-edgecenter-webhook:latest`. It is **highly recommended** to replace `latest` with a specific version tag for predictable deployments. Edit the manifest or use tools like Kustomize to set the desired image tag.
    *   The `livenessProbe` and `readinessProbe` in the manifest correctly point to the `/health` endpoint on port `8888`.

3.  **Configure ExternalDNS:** Deploy ExternalDNS (if not already running) and configure it to use the webhook provider. Point it to the webhook service URL: `http://external-dns-edgecenter-webhook.external-dns.svc.cluster.local:8888` (adjust namespace if needed).

    Example ExternalDNS arguments:
    ```yaml
          args:
            - --source=service
            - --source=ingress
            - --provider=webhook
            - --webhook-provider-url=http://external-dns-edgecenter-webhook.external-dns:8888 # Adjust namespace
            - --registry=txt
            - --txt-owner-id=my-edgecenter-dns # Unique ID for this instance
            # - --domain-filter=example.com # Optional: Filter managed domains
            # - --annotation-filter=external-dns.alpha.kubernetes.io/target-provider=edgecenter # Optional: For multi-provider setups
    ```

### Using Terraform

The repository includes example Terraform configurations for deploying the webhook and ExternalDNS itself.

-**(Note: The following Terraform example is quite extensive. Consider moving it to a separate file like `docs/terraform.md` for better readability.)**

#### Prerequisites

1.  Ensure you have Terraform and the Kubernetes provider configured.
2.  Create a Kubernetes secret for the EdgeCenter API key (named `token` in this example). Choose one method:

    **Method 1: Regular Kubernetes Secret**

    ```bash
    kubectl create secret generic edgecenter-webhook-secret \
      --from-literal=token='YOUR_EDGECENTER_API_KEY' \
      --namespace external-dns # Specify your namespace
    ```

    **Method 2: Integration with External Secrets Operator**

    If you use the External Secrets Operator, configure an `ExternalSecret` (see [docs/terraform.md](docs/terraform.md) for the full example).

#### Example Terraform Configuration

For detailed examples of Terraform variables and resources (`kubernetes_deployment` for the webhook and ExternalDNS, `kubernetes_service`), please refer to the dedicated documentation:

**[docs/terraform.md](docs/terraform.md)**

#### Applying the Configuration

```bash
terraform init
terraform plan
terraform apply
```

## Configuration

The webhook behavior can be configured via environment variables:

| Environment Variable | Terraform Variable | Description                                       | Default Value                     |
|----------------------|--------------------|---------------------------------------------------|-----------------------------------|
| `EDGECENTER_API_KEY` | (from secret)      | **Required.** EdgeCenter API access key/token.    | -                                 |
| `EDGECENTER_BASE_URL`|                    | Optional. Custom base URL for the EdgeCenter API. | `https://api.edgecenter.ru/dns` |
| `DRY_RUN`            | `dry_run`          | Enable dry-run mode (logs changes, doesn't apply) | `false`                           |
| `DEFAULT_TTL`        |                    | Optional. Default TTL for DNS records (seconds).  | `600`                             |
| `PORT`               | (in code)          | Internal port the webhook server listens on.      | `8888`                            |
| `LOG_LEVEL`          | (in code)          | Logging level (internal zap logger).              | `info`                            |

*(Note: `PORT` and `LOG_LEVEL` are currently hardcoded but could be made configurable via environment variables if needed)*

## Usage

Once the webhook is deployed and ExternalDNS is configured to use it, ExternalDNS will watch for annotated Kubernetes resources (Services, Ingresses) and create corresponding DNS records in EdgeCenter.

### Basic Example

Annotate your Service or Ingress object with `external-dns.alpha.kubernetes.io/hostname`:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: my-app-service
  annotations:
    external-dns.alpha.kubernetes.io/hostname: myapp.example.com. # Note the trailing dot!
spec:
  type: LoadBalancer # Or NodePort, or ClusterIP if using Ingress
  ports:
  - port: 80
  selector:
    app: my-app
```

ExternalDNS will detect this service and instruct the webhook to create an A record (or AAAA, depending on the service's IP) for `myapp.example.com` pointing to the external IP address of the LoadBalancer.

### Using with Multiple DNS Providers

This webhook supports running alongside other ExternalDNS instances managing different providers (e.g., Yandex Cloud DNS, AWS Route53). This is achieved using the `--annotation-filter` argument in ExternalDNS.

1.  **Deploy the EdgeCenter Webhook** as described above.
2.  **Deploy ExternalDNS for EdgeCenter:** Configure this instance with `--provider=webhook`, `--webhook-provider-url=...`, and `--annotation-filter=external-dns.alpha.kubernetes.io/target-provider=edgecenter` (or your chosen annotation).
3.  **Deploy ExternalDNS for the Other Provider:** Configure this second instance with its specific provider settings (e.g., `--provider=yandex`) and **omit** the `--annotation-filter` (or use a different one).
4.  **Annotate Resources:**
    *   For records in EdgeCenter, add **both** the hostname annotation and the target provider annotation:
        ```yaml
        apiVersion: v1
        kind: Service
        metadata:
          name: app-edgecenter
          annotations:
            external-dns.alpha.kubernetes.io/hostname: app.edgecenter.example.com.
            external-dns.alpha.kubernetes.io/target-provider: edgecenter
        # ...
        ```
    *   For records in the other provider, add only the hostname annotation:
        ```yaml
        apiVersion: v1
        kind: Service
        metadata:
          name: app-other-provider
          annotations:
            external-dns.alpha.kubernetes.io/hostname: app.other.example.com.
            # No target-provider annotation
        # ...
        ```

Refer to the extensive examples and RBAC setup in the [Terraform section](#using-terraform) (or previous revisions of this README if the section was shortened).

## Webhook API

The webhook exposes the following endpoints conforming to the [ExternalDNS Webhook Specification](https://github.com/kubernetes-sigs/external-dns/blob/master/docs/proposal/webhook.md):

-   `GET /`: Returns domain filter information. ExternalDNS uses this for initialization.
-   `GET /records`: Returns all managed DNS records from EdgeCenter matching the domain filter.
-   `POST /records`: Applies DNS changes (Create, Update, Delete) received from ExternalDNS.
-   `POST /adjustendpoints`: Allows the provider to modify proposed endpoints before they are planned (currently returns endpoints unmodified).
-   `GET /health`: Health check endpoint. Returns `OK` with status `200`.

## Development

### Prerequisites

- Go 1.24+
- Docker

### Setup

```bash
# Install Go dependencies
go mod download
go mod tidy
```

### Running Tests

```bash
# Run all tests
go test ./...

# Run tests with coverage report
go test -cover ./...

# Generate HTML coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

### Running Locally

```bash
# Ensure EDGECENTER_API_KEY environment variable is set
export EDGECENTER_API_KEY="your-api-key"

# Optional: Set a different base URL
# export EDGECENTER_BASE_URL="https://..."

# Run the webhook (listens on port 8888 by default)
go run ./main.go
```

You can then configure a local ExternalDNS instance to point to `http://localhost:8888`.

### Contribution Guidelines

*(TODO: Add guidelines for contributing, code style, pull requests, etc.)*

## Limitations

### Subzone Record Management

When managing records in subzones (e.g., `sub.example.com`) using ExternalDNS with `registry=txt` (the default), be aware of where the associated TXT records are created:

1.  **TXT Records Requirement:** ExternalDNS creates an associated TXT record (e.g., `a-sub.example.com`) for each managed record (e.g., A record for `sub.example.com`) to store metadata.
2.  **TXT Record Placement Logic:**
    *   The TXT record for a regular DNS record (e.g., `a-sub.test.example.com` for `sub.test.example.com`) resides **in the same zone** (`test.example.com`).
    *   The TXT record managing the zone itself (e.g., `a-test.example.com` for the zone `test.example.com`) resides **in the parent zone** (`example.com`). This is a standard ExternalDNS behavior.
3.  **API Permission Implication:** To manage records in `test.example.com`, your EdgeCenter API key needs permissions for that zone. To allow ExternalDNS to manage the zone itself (via the TXT record like `a-test.example.com`), the API key **also needs permissions for the parent zone** (`example.com`) where that specific TXT record will be created.

**Consequence:** If your API key lacks permissions for the parent zone (`example.com`), ExternalDNS **will fail** to create the ownership TXT record (`a-test.example.com`) and may not manage records in the subzone (`test.example.com`) correctly.

**Recommendations:**

*   **Use an API Key with Broad Permissions:** Grant the API key permissions for all relevant zones and their parent zones where ownership TXT records might be created.
*   **Consider `--txt-prefix` (with caution):** This ExternalDNS flag changes TXT record names (e.g., `myprefix-a-test.example.com`) causing them to be created *in the same zone* (`test.example.com`) instead of the parent zone. This avoids needing parent zone permissions *but* might interfere with zone management if the API specifically expects the un-prefixed TXT record name. Test thoroughly if using this flag.
*   **Explore Other Registries:** Using a different ExternalDNS registry (e.g., `crd`) might avoid the need for TXT records if permissions are a constraint.

## Troubleshooting

-   **Webhook Not Running/Restarting:** Check deployment logs (`kubectl logs deployment/external-dns-edgecenter-webhook -n <namespace>`), check for CrashLoopBackOff, ensure the `EDGECENTER_API_KEY` secret exists and is correctly referenced. Verify resource limits/requests.
-   **ExternalDNS Errors (Timeout, Connection Refused):** Ensure the webhook Service is running (`kubectl get svc -n <namespace>`) and reachable from the ExternalDNS pod. Check the `--webhook-provider-url` argument in ExternalDNS. Check network policies.
-   **Health Checks Failing:** Verify the health check path in the Deployment manifest matches the one implemented (`/health`). Check webhook logs for errors during startup or request handling.
-   **Records Not Created/Updated:**
    *   Enable debug logging in ExternalDNS (`--log-level=debug`).
    *   Check ExternalDNS logs for errors related to applying changes or calling the webhook.
    *   Check webhook logs for errors received from the EdgeCenter API (e.g., permission denied, invalid record).
    *   Verify the API key has the necessary permissions for the target DNS zones.
    *   Check for parent zone permission issues if managing subzones (see Limitations).
    *   Ensure annotations on Services/Ingresses are correct (hostname, target-provider if used).
    *   Check if `DRY_RUN` mode is enabled in the webhook (`DRY_RUN=true`).
-   **Incorrect Records Created:** Check webhook logs and `provider.go` logic, especially `getZoneAndRecordName`.
```