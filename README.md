# EdgeCenter DNS Webhook for ExternalDNS

Этот проект представляет собой реализацию webhook-провайдера для [ExternalDNS](https://github.com/kubernetes-sigs/external-dns), который позволяет управлять DNS-записями в EdgeCenter DNS.

## Особенности

- Полная поддержка API ExternalDNS Webhook
- Управление DNS-записями в EdgeCenter DNS
- Поддержка всех типов DNS-записей
- Режим dry-run для безопасной отладки изменений DNS
- Фильтрация по аннотациям для работы с несколькими экземплярами ExternalDNS
- Интеграция с External Secrets Operator для безопасного управления секретами (опционально)

## Требования

- Go 1.19 или выше (для сборки)
- Docker (для сборки образа)
- Kubernetes 1.16 или выше
- ExternalDNS v0.13.0 или выше
- Доступ к API EdgeCenter DNS
- Terraform v1.0 или выше (для развертывания)
- Kubectl

## Сборка Docker-образа

```bash
# Склонируйте репозиторий
git clone https://viory.gitlab.yandexcloud.net/viory/external-dns-edgecenter-webhook.git
cd external-dns-edgecenter-webhook

# Соберите Docker-образ
# Замените <your-registry> и <tag> на свои значения
docker build -t <your-registry>/external-dns-edgecenter-webhook:<tag> .

# Загрузите образ в ваш Docker-репозиторий
docker push <your-registry>/external-dns-edgecenter-webhook:<tag>
```

## Развертывание с помощью Terraform

### Предварительные требования

1.  Убедитесь, что у вас настроен Terraform и провайдер Kubernetes.
2.  Создайте секрет Kubernetes для токена EdgeCenter.

    **Способ 1: Обычный секрет Kubernetes**

    ```bash
    kubectl create secret generic edgecenter-credentials \
      --from-literal=token='YOUR_EDGECENTER_API_TOKEN' \
      --namespace external-dns # Укажите ваш namespace
    ```

    **Способ 2: Интеграция с External Secrets Operator**

    Если вы используете External Secrets Operator, настройте `ExternalSecret`:

    ```terraform
    resource "kubernetes_manifest" "external_secret_edgecenter" {
      manifest = {
        "apiVersion" = "external-secrets.io/v1beta1"
        "kind"       = "ExternalSecret"
        "metadata" = {
          "name"      = "edgecenter-webhook-secret"
          "namespace" = var.namespace # Ваш namespace
        }
        "spec" = {
          "refreshInterval" = "1h"
          "secretStoreRef" = {
            "name" = "vault-backend" # Имя вашего SecretStore
            "kind" = "ClusterSecretStore"
          }
          "target" = {
            "name" = "edgecenter-webhook-secret" # Имя секрета Kubernetes, который будет создан
          }
          "data" = [
            {
              "secretKey" = "token"
              "remoteRef" = {
                "key"      = "external-dns/edgecenter" # Путь к секрету в вашем хранилище
                "property" = "token"                   # Имя поля в секрете
              }
            }
          ]
        }
      }
    }
    ```

### Пример Terraform конфигурации

```terraform
variable "namespace" {
  description = "Namespace for deployment"
  default     = "external-dns"
}

variable "webhook_image" {
  description = "Docker image for the webhook"
  default     = "cr.yandex/crpminendqjcho56q23n/external-dns-edgecenter-webhook:build.593-branch.master" # Укажите ваш образ!
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
  # Конфигурация вашего Kubernetes провайдера
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
        service_account_name = "default" # Укажите ваш Service Account, если нужно

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
                # Укажите имя секрета, созданного вручную или через ExternalSecret
                name = "edgecenter-webhook-secret" # или "edgecenter-credentials"
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
      port        = 8888 # Порт, который слушает ExternalDNS
      target_port = "http" # Имя порта в Deployment
      protocol    = "TCP"
      name        = "http"
    }
    type = "ClusterIP"
  }
}

resource "kubernetes_deployment" "external_dns_edgecenter" {
  metadata {
    name      = "external-dns-edgecenter"
    namespace = var.namespace
    labels = {
      app = "external-dns-edgecenter"
    }
  }
  spec {
    replicas = 1 # Можно сделать переменной
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
        service_account_name = kubernetes_service_account.external_dns.metadata.0.name # Используем созданный SA
        container {
          name  = "external-dns"
          image = "registry.k8s.io/external-dns/external-dns:v0.13.0"
          args = [
            "--source=service",
            "--source=ingress",
            "--provider=webhook",
            "--webhook-provider-url=http://external-dns-edgecenter-webhook:8888", # Адрес сервиса нашего webhook
            "--annotation-filter=external-dns.alpha.kubernetes.io/target-provider=edgecenter",
            "--registry=txt",
            "--txt-owner-id=external-dns-edgecenter",
            "--request-timeout=2m", # Увеличенный таймаут для запросов к API
            "--no-nodes"            # Отключение синхронизации Node-ресурсов
          ]
          # Добавьте ресурсы и пробы по необходимости
        }
      }
    }
  }
}

resource "kubernetes_deployment" "external_dns_yandex" {
  metadata {
    name      = "external-dns-yandex"
    namespace = var.namespace
    labels = {
      app = "external-dns-yandex"
    }
  }
  spec {
    replicas = 1 # Можно сделать переменной
    selector {
      match_labels = {
        app = "external-dns-yandex"
      }
    }
    template {
      metadata {
        labels = {
          app = "external-dns-yandex"
        }
      }
      spec {
        service_account_name = kubernetes_service_account.external_dns.metadata.0.name # Используем созданный SA
        container {
          name  = "external-dns"
          image = "registry.k8s.io/external-dns/external-dns:v0.13.0"
          args = [
            "--source=service",
            "--source=ingress",
            "--provider=yandex",
            "--registry=txt",
            "--txt-owner-id=external-dns-yandex",
            "--request-timeout=2m", # Увеличенный таймаут для запросов к API
            "--no-nodes"            # Отключение синхронизации Node-ресурсов
          ]
          env {
            name = "YANDEX_CLOUD_FOLDER_ID"
            value_from {
              secret_key_ref {
                name = "yandex-dns-credentials"
                key  = "folder-id"
              }
            }
          }
          env {
            name = "YANDEX_CLOUD_TOKEN"
            value_from {
              secret_key_ref {
                name = "yandex-dns-credentials"
                key  = "token"
              }
            }
          }
          # Добавьте ресурсы и пробы по необходимости
        }
      }
    }
  }
}
```

### Применение конфигурации

```bash
terraform init
terraform plan
terraform apply
```

## Конфигурация

Основные параметры управляются через переменные окружения в манифесте Deployment:

| Переменная        | Terraform переменная | Описание                                                | Значение по умолчанию                                         |
|-------------------|----------------------|---------------------------------------------------------|---------------------------------------------------------------|
| `DRY_RUN`         | `dry_run`            | Включить режим dry-run (без внесения изменений в DNS) | `false`                                                       |
| `EDGECENTER_API_KEY`| (из секрета)         | Токен доступа к API EdgeCenter                           | -                                                             |
| `PORT`            | (в коде)             | Порт, на котором слушает webhook сервер               | `8080`                                                        |
| `LOG_LEVEL`       | (в коде)             | Уровень логирования (debug, info, warn, error)        | `info`                                                        |

## Использование с несколькими DNS-провайдерами

### Общее описание

Если в вашем кластере уже работает ExternalDNS с другим провайдером (например, Yandex DNS), вы можете настроить работу обоих провайдеров одновременно. Для этого нужно развернуть два экземпляра ExternalDNS:

1.  **ExternalDNS для EdgeCenter:** Настроен на использование webhook-провайдера и фильтрует ресурсы по аннотации `external-dns.alpha.kubernetes.io/target-provider=edgecenter`.
2.  **ExternalDNS для Yandex:** Настроен на использование Yandex-провайдера и *не* использует фильтр по аннотациям (обрабатывает все остальные ресурсы).

### Настройка RBAC для ExternalDNS (Terraform)

ExternalDNS требует прав на чтение Services, Ingresses, Endpoints, Pods и Nodes в кластере. Создайте необходимые RBAC ресурсы:

```terraform
resource "kubernetes_service_account" "external_dns" {
  metadata {
    name      = "external-dns"
    namespace = var.namespace
    # Можно добавить аннотации для IAM-ролей (например, для AWS IRSA или GCP Workload Identity)
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
    resources  = ["services", "endpoints", "pods", "nodes"]
    verbs      = ["get", "watch", "list"]
  }

  rule {
    api_groups = ["extensions", "networking.k8s.io"]
    resources  = ["ingresses"]
    verbs      = ["get", "watch", "list"]
  }
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

### Настройка ExternalDNS для EdgeCenter (через Terraform)

```terraform
resource "kubernetes_deployment" "external_dns_edgecenter" {
  metadata {
    name      = "external-dns-edgecenter"
    namespace = var.namespace
    labels = {
      app = "external-dns-edgecenter"
    }
  }
  spec {
    replicas = 1 # Можно сделать переменной
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
        service_account_name = kubernetes_service_account.external_dns.metadata.0.name # Используем созданный SA
        container {
          name  = "external-dns"
          image = "registry.k8s.io/external-dns/external-dns:v0.13.0"
          args = [
            "--source=service",
            "--source=ingress",
            "--provider=webhook",
            "--webhook-provider-url=http://external-dns-edgecenter-webhook:8888", # Адрес сервиса нашего webhook
            "--annotation-filter=external-dns.alpha.kubernetes.io/target-provider=edgecenter",
            "--registry=txt",
            "--txt-owner-id=external-dns-edgecenter",
            "--request-timeout=2m", # Увеличенный таймаут для запросов к API
            "--no-nodes"            # Отключение синхронизации Node-ресурсов
          ]
          # Добавьте ресурсы и пробы по необходимости
        }
      }
    }
  }
}
```

### Настройка ExternalDNS для Yandex (через Terraform)

Сначала создайте секрет с учетными данными Yandex Cloud (см. секцию "Предварительные требования"). Затем создайте Deployment:

```terraform
resource "kubernetes_deployment" "external_dns_yandex" {
  metadata {
    name      = "external-dns-yandex"
    namespace = var.namespace
    labels = {
      app = "external-dns-yandex"
    }
  }
  spec {
    replicas = 1 # Можно сделать переменной
    selector {
      match_labels = {
        app = "external-dns-yandex"
      }
    }
    template {
      metadata {
        labels = {
          app = "external-dns-yandex"
        }
      }
      spec {
        service_account_name = kubernetes_service_account.external_dns.metadata.0.name # Используем созданный SA
        container {
          name  = "external-dns"
          image = "registry.k8s.io/external-dns/external-dns:v0.13.0"
          args = [
            "--source=service",
            "--source=ingress",
            "--provider=yandex",
            "--registry=txt",
            "--txt-owner-id=external-dns-yandex",
            "--request-timeout=2m", # Увеличенный таймаут для запросов к API
            "--no-nodes"            # Отключение синхронизации Node-ресурсов
          ]
          env {
            name = "YANDEX_CLOUD_FOLDER_ID"
            value_from {
              secret_key_ref {
                name = "yandex-dns-credentials"
                key  = "folder-id"
              }
            }
          }
          env {
            name = "YANDEX_CLOUD_TOKEN"
            value_from {
              secret_key_ref {
                name = "yandex-dns-credentials"
                key  = "token"
              }
            }
          }
          # Добавьте ресурсы и пробы по необходимости
        }
      }
    }
  }
}
```

### Использование

После настройки вы можете управлять DNS-записями через аннотации в ваших сервисах и ингрессах:

```yaml
# Пример 1: Запись в EdgeCenter DNS
apiVersion: v1
kind: Service
metadata:
  name: app-edgecenter
  annotations:
    external-dns.alpha.kubernetes.io/hostname: app.example.com
    external-dns.alpha.kubernetes.io/target-provider: edgecenter  # Указываем провайдер EdgeCenter
spec:
  type: LoadBalancer
  ports:
  - port: 80
  selector:
    app: my-app

---
# Пример 2: Запись в Yandex DNS
apiVersion: v1
kind: Service
metadata:
  name: app-yandex
  annotations:
    external-dns.alpha.kubernetes.io/hostname: app.example.ru
    # Не указываем target-provider, запись будет обработана Yandex DNS
spec:
  type: LoadBalancer
  ports:
  - port: 80
  selector:
    app: my-app

# ... (Примеры с Ingress аналогичны)
```

### Примеры YAML-манифестов для тестирования

#### 1. Тестовое приложение Nginx с сервисом и двумя ingress (EdgeCenter и Yandex)

```yaml
---
# Deployment для тестового приложения
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-test
  namespace: external-dns
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
# Service для тестового приложения
apiVersion: v1
kind: Service
metadata:
  name: nginx-test
  namespace: external-dns
spec:
  ports:
  - port: 80
    targetPort: http
    protocol: TCP
    name: http
  selector:
    app: nginx-test

---
# Ingress для EdgeCenter DNS
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: nginx-test-edgecenter
  namespace: external-dns
  annotations:
    external-dns.alpha.kubernetes.io/target-provider: edgecenter
    kubernetes.io/ingress.class: nginx
    # Опциональные аннотации для NGINX Ingress Controller
    # nginx.ingress.kubernetes.io/ssl-redirect: "false"
    # nginx.ingress.kubernetes.io/use-regex: "true"
spec:
  rules:
  - host: nginx-edge.test.example.com  # Замените на ваш домен
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
# Ingress для Yandex DNS (без аннотации target-provider)
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: nginx-test-yandex
  namespace: external-dns
  annotations:
    kubernetes.io/ingress.class: nginx
    # Опциональные аннотации для NGINX Ingress Controller
    # nginx.ingress.kubernetes.io/ssl-redirect: "false"
    # nginx.ingress.kubernetes.io/use-regex: "true"
spec:
  rules:
  - host: nginx-yandex.test.example.ru  # Замените на ваш домен
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

#### 2. Тестовое приложение с TLS (HTTPS)

```yaml
---
# Deployment и Service аналогичны предыдущему примеру
# Секрет с TLS-сертификатом (замените на ваши данные)
apiVersion: v1
kind: Secret
metadata:
  name: tls-secret-test
  namespace: external-dns
type: kubernetes.io/tls
data:
  # Замените на ваши закодированные в base64 сертификат и ключ
  tls.crt: LS0tLS1CRUdJTi...
  tls.key: LS0tLS1CRUdJTi...

---
# Ingress с TLS для EdgeCenter DNS
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: tls-test-edgecenter
  namespace: external-dns
  annotations:
    external-dns.alpha.kubernetes.io/target-provider: edgecenter
    kubernetes.io/ingress.class: nginx
spec:
  tls:
  - hosts:
    - secure-edge.test.example.com
    secretName: tls-secret-test
  rules:
  - host: secure-edge.test.example.com  # Замените на ваш домен
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

#### 3. Команды для применения и проверки

```bash
# Применение манифестов
kubectl apply -f test-manifests.yaml

# Проверка созданных ресурсов
kubectl get deploy,svc,ing -n external-dns

# Проверка логов EdgeCenter ExternalDNS
kubectl logs -f deployment/external-dns-edgecenter -n external-dns

# Проверка логов Yandex ExternalDNS
kubectl logs -f deployment/external-dns-yandex -n external-dns

# Проверка логов EdgeCenter Webhook
kubectl logs -f deployment/external-dns-edgecenter-webhook -n external-dns
```

После создания ресурсов, ExternalDNS должен обнаружить новые Ingress и создать соответствующие DNS-записи в EdgeCenter DNS и Yandex DNS.

### Принцип работы

1.  **EdgeCenter ExternalDNS:**
    *   Обрабатывает только ресурсы с аннотацией `external-dns.alpha.kubernetes.io/target-provider=edgecenter`.
    *   Отправляет запросы на webhook `external-dns-edgecenter-webhook`.
2.  **Yandex ExternalDNS:**
    *   Обрабатывает все остальные ресурсы (без этой аннотации).
    *   Напрямую взаимодействует с API Yandex Cloud.
3.  **EdgeCenter Webhook:**
    *   Получает запросы от EdgeCenter ExternalDNS.
    *   Взаимодействует с API EdgeCenter DNS для создания/обновления/удаления записей (или логирует в режиме `dryRun`).

### Рекомендации

1.  Используйте разные `txt-owner-id` для каждого экземпляра ExternalDNS.
2.  Проверяйте логи обоих экземпляров ExternalDNS и webhook при отладке.
3.  При первом развертывании используйте режим `dryRun=true` для проверки.
4.  Следите за правильностью аннотаций в ваших ресурсах.
5.  Рекомендуется использовать разные домены для разных провайдеров, если это применимо.
6.  Убедитесь, что у вас настроены правильные разрешения (IAM) для обоих провайдеров.
7.  Используйте разные неймспейсы для разных провайдеров, если это возможно.

### Отладка

Для проверки работы мультипровайдерной конфигурации:

```bash
# Проверка статуса подов
kubectl get pods -n external-dns

# Проверка логов EdgeCenter ExternalDNS
kubectl logs -f deployment/external-dns-edgecenter -n external-dns

# Проверка логов Yandex ExternalDNS
kubectl logs -f deployment/external-dns-yandex -n external-dns

# Проверка логов EdgeCenter Webhook
kubectl logs -f deployment/external-dns-edgecenter-webhook -n external-dns

# Проверка созданных DNS-записей в EdgeCenter
# Замените $TOKEN и $ZONE_ID
curl -H "Authorization: Bearer $TOKEN" https://api.edgecenter.ru/dns/v2/zones/$ZONE_ID/records

# Проверка созданных DNS-записей в Yandex Cloud
# Замените $ZONE_NAME
yc dns zone list-records --name=$ZONE_NAME
```

### Известные проблемы

1.  При использовании нескольких провайдеров убедитесь, что у вас нет конфликтов в TXT-записях для registry.
2.  Если вы используете разные домены для разных провайдеров, убедитесь, что `domain-filter` (если используется) настроен правильно в args ExternalDNS.
3.  При переключении провайдера для существующей записи может потребоваться ручное удаление старой записи.
4.  Возможны задержки при обновлении DNS-записей из-за кэширования DNS и TTL.
5.  При использовании режима `dryRun=true` записи не будут созданы, но в логах вы увидите, какие изменения были бы применены.


### Устранение неполадок

Если у вас возникли проблемы:

1.  **Проверьте логи всех компонентов:**
    *   EdgeCenter ExternalDNS
    *   Yandex ExternalDNS
    *   EdgeCenter Webhook
    *   Kubernetes Events (`kubectl get events -n external-dns`)

2.  **Убедитесь, что все секреты настроены правильно:**
    ```bash
    # Проверка секрета EdgeCenter
    kubectl get secret -n external-dns edgecenter-webhook-secret -o yaml # или edgecenter-credentials

    # Проверка секрета Yandex
    kubectl get secret -n external-dns yandex-dns-credentials -o yaml
    ```

3.  **Проверьте настройки RBAC** (если используете отдельные Service Accounts):
    ```bash
    kubectl describe clusterrolebinding <binding-name>
    kubectl describe serviceaccount <sa-name> -n external-dns
    ```

4.  **Проверьте сетевую доступность:**
    ```bash
    # От пода EdgeCenter ExternalDNS к Webhook
    kubectl exec -it deploy/external-dns-edgecenter -n external-dns -- wget -qO- http://external-dns-edgecenter-webhook:8888/health

    # От пода Yandex ExternalDNS к API Yandex
    kubectl exec -it deploy/external-dns-yandex -n external-dns -- wget -T 5 -qO- https://api.cloud.yandex.net/

    # От пода Webhook к API EdgeCenter
    kubectl exec -it deploy/external-dns-edgecenter-webhook -n external-dns -- wget -T 5 -qO- https://api.edgecenter.ru/
    ```

5.  **Проверьте конфигурацию ресурсов:**
    ```bash
    # Проверка аннотаций на сервисах
    kubectl get svc -A -o custom-columns=NAME:.metadata.name,NS:.metadata.namespace,ANNOTATIONS:.metadata.annotations

    # Проверка аннотаций на ингрессах
    kubectl get ing -A -o custom-columns=NAME:.metadata.name,NS:.metadata.namespace,ANNOTATIONS:.metadata.annotations
    ```

## Безопасность

1.  Используйте External Secrets или другой безопасный механизм для управления токеном EdgeCenter.
2.  Настройте RBAC для ограничения доступа Service Accounts.
3.  Запускайте контейнер от имени непривилегированного пользователя (`securityContext`).
4.  Регулярно обновляйте токены доступа.

## API

Webhook реализует стандартный [ExternalDNS Webhook Provider API](https://github.com/kubernetes-sigs/external-dns/blob/master/docs/proposal/webhook.md).

## Разработка

Инструкции по локальной разработке и тестированию...

## Лицензия

MIT License 