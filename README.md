# EdgeCenter DNS Webhook for ExternalDNS

Этот проект представляет собой реализацию webhook-провайдера для [ExternalDNS](https://github.com/kubernetes-sigs/external-dns), который позволяет управлять DNS-записями в EdgeCenter DNS.

## Особенности

- Полная поддержка API ExternalDNS Webhook
- Управление DNS-записями в EdgeCenter DNS
- Поддержка всех типов DNS-записей
- Фильтрация по доменам
- Интеграция с External Secrets Operator для безопасного управления секретами
- Режим dry-run для безопасной отладки изменений
- Helm chart для простого развертывания в Kubernetes

## Требования

- Go 1.19 или выше
- Kubernetes 1.16 или выше
- ExternalDNS v0.13.0 или выше
- External Secrets Operator v0.9.0 или выше
- Доступ к API EdgeCenter DNS

## Установка

### Предварительные требования

1. Установите External Secrets Operator:
```bash
helm repo add external-secrets https://charts.external-secrets.io
helm install external-secrets external-secrets/external-secrets \
  --namespace external-secrets \
  --create-namespace
```

2. Настройте SecretStore или ClusterSecretStore для вашего хранилища секретов (например, HashiCorp Vault):
```yaml
apiVersion: external-secrets.io/v1beta1
kind: ClusterSecretStore
metadata:
  name: vault-backend
spec:
  provider:
    vault:
      server: "https://vault.example.com"
      path: "secret"
      version: "v2"
      auth:
        kubernetes:
          mountPath: "kubernetes"
          role: "external-secrets"
          serviceAccountRef:
            name: "external-secrets"
            namespace: "external-secrets"
```

### Установка чарта

```bash
# Добавляем репозиторий Helm
helm repo add external-dns-edgecenter-webhook https://example.com/charts
helm repo update

# Устанавливаем chart
helm install external-dns-edgecenter-webhook \
  --namespace external-dns \
  --set externalSecrets.secretStore.name=vault-backend \
  --set externalSecrets.remoteRef.key=external-dns/edgecenter \
  external-dns-edgecenter-webhook/external-dns-edgecenter-webhook
```

### Конфигурация External Secrets

Для работы с External Secrets необходимо:

1. Создать секрет в вашем хранилище (например, в Vault):
```bash
vault kv put secret/external-dns/edgecenter \
  token=your-edgecenter-api-token
```

2. Убедиться, что External Secrets Operator имеет доступ к этому секрету.

## Конфигурация

### Основные параметры

| Параметр | Описание | Значение по умолчанию |
|----------|-----------|----------------------|
| `image.repository` | Репозиторий образа | `ghcr.io/your-org/external-dns-edgecenter-webhook` |
| `image.tag` | Тег образа | `""` (использует appVersion) |
| `image.pullPolicy` | Политика загрузки образа | `IfNotPresent` |
| `replicaCount` | Количество реплик | `1` |
| `dryRun` | Режим dry-run для отладки | `false` |
| `annotationFilter` | Фильтр по аннотациям для ExternalDNS | `external-dns.alpha.kubernetes.io/target-provider=edgecenter` |

### Режим Dry-Run

Режим dry-run позволяет безопасно тестировать изменения DNS без фактического применения их в EdgeCenter DNS. В этом режиме все операции только логируются, но не выполняются.

Для включения режима dry-run:

```bash
helm install external-dns-edgecenter-webhook \
  --namespace external-dns \
  --set dryRun=true \
  external-dns-edgecenter-webhook/external-dns-edgecenter-webhook
```

В логах вы увидите, какие изменения были бы применены:
```
INFO would create record {"zone": "example.com", "record": "test", "type": "A", "content": "192.0.2.1", "ttl": 3600}
INFO would update record {"zone": "example.com", "record": "www", "type": "CNAME", "targets": ["example.com"], "ttl": 3600}
INFO would delete record {"zone": "example.com", "record": "old", "type": "A"}
```

### External Secrets

| Параметр | Описание | Значение по умолчанию |
|----------|-----------|----------------------|
| `externalSecrets.enabled` | Включить External Secrets | `true` |
| `externalSecrets.refreshInterval` | Интервал обновления секрета | `1h` |
| `externalSecrets.secretStore.name` | Имя SecretStore | `vault-backend` |
| `externalSecrets.secretStore.kind` | Тип хранилища | `ClusterSecretStore` |
| `externalSecrets.remoteRef.key` | Путь к секрету | `external-dns/edgecenter` |
| `externalSecrets.remoteRef.property` | Имя поля в секрете | `token` |

## Безопасность

Для обеспечения безопасности:

1. Используйте External Secrets для управления токеном EdgeCenter
2. Включите режим dry-run при первом развертывании или тестировании изменений
3. Ограничьте доступ к API EdgeCenter с помощью RBAC
4. Регулярно обновляйте токен EdgeCenter

## Поддержка

При возникновении проблем:

1. Проверьте логи с помощью `kubectl logs`
2. Включите режим dry-run для отладки изменений
3. Создайте issue в репозитории проекта

## Лицензия

MIT License

## Использование с ExternalDNS

1. Установите webhook провайдер:
```bash
# Обычная установка
helm install external-dns-edgecenter-webhook ...

# Установка в режиме dry-run для отладки
helm install external-dns-edgecenter-webhook \
  --namespace external-dns \
  --set dryRun=true \
  external-dns-edgecenter-webhook/external-dns-edgecenter-webhook
```

2. Настройте ExternalDNS для использования webhook провайдера:
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: external-dns
spec:
  template:
    spec:
      containers:
      - name: external-dns
        image: registry.k8s.io/external-dns/external-dns:v0.13.0
        args:
        - --source=service
        - --source=ingress
        - --provider=webhook
        - --webhook-provider-url=http://external-dns-edgecenter-webhook:8888
        - --domain-filter=example.com
```

## Примеры

### Создание DNS-записи через Service

```yaml
apiVersion: v1
kind: Service
metadata:
  name: nginx
  annotations:
    external-dns.alpha.kubernetes.io/hostname: nginx.example.com
spec:
  type: LoadBalancer
  ports:
  - port: 80
  selector:
    app: nginx
```

### Создание DNS-записи через Ingress

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: nginx
spec:
  rules:
  - host: nginx.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: nginx
            port:
              number: 80
```

## API

Webhook реализует следующие эндпоинты в соответствии со спецификацией ExternalDNS Webhook API v0.15.0:

- `GET /` - Инициализация и согласование заголовков, возвращает фильтры доменов
  - Ответ: Список доменов, которые обслуживает DNS-провайдер
  - Формат: `application/external.dns.webhook+json;version=1`

- `GET /records` - Получение текущих DNS-записей
  - Ответ: Список текущих DNS-записей с их параметрами (имя, TTL, тип, цели)
  - Формат: `application/external.dns.webhook+json;version=1`

- `POST /records` - Применение изменений DNS-записей
  - Тело запроса: Список изменений (создание, удаление, обновление записей)
  - Формат: `application/external.dns.webhook+json;version=1`
  - Структура изменений:
    - `create`: Записи для создания
    - `delete`: Записи для удаления
    - `updateOld`: Старые версии записей для обновления
    - `updateNew`: Новые версии записей для обновления

- `POST /adjustendpoints` - Корректировка эндпоинтов
  - Тело запроса: Список записей для корректировки
  - Ответ: Скорректированный список записей
  - Формат: `application/external.dns.webhook+json;version=1`

Поддерживаемые типы DNS-записей:
- A
- CNAME
- TXT
- и другие стандартные типы DNS-записей

Каждая DNS-запись может содержать:
- `dnsName`: Имя записи (например, "test.example.com")
- `recordType`: Тип записи (A, CNAME, etc.)
- `recordTTL`: Время жизни записи в секундах
- `targets`: Массив целевых значений (IP-адреса для A-записей, имена для CNAME и т.д.)
- `setIdentifier`: Опциональный идентификатор для записи
- `labels`: Дополнительные метки в формате ключ-значение
- `providerSpecific`: Специфичные для провайдера параметры

Подробная спецификация API доступна в [docs/api/webhook.yaml](docs/api/webhook.yaml).

## Разработка

```bash
# Запуск тестов
go test -v ./...

# Сборка
go build

# Запуск с отладкой
LOG_LEVEL=debug ./external-dns-edgecenter-webhook

# Запуск в режиме dry-run для отладки
LOG_LEVEL=debug DRY_RUN=true ./external-dns-edgecenter-webhook
```

## Использование с несколькими DNS-провайдерами

### Общее описание

Если в вашем кластере уже работает ExternalDNS с другим провайдером (например, Yandex DNS), вы можете настроить работу обоих провайдеров одновременно. Для этого нужно:

1. Установить EdgeCenter Webhook провайдер
2. Настроить два экземпляра ExternalDNS:
   - Один для EdgeCenter DNS (с фильтром по аннотациям)
   - Один для Yandex DNS (без фильтра, будет обрабатывать все остальные записи)

### Установка EdgeCenter Webhook

```bash
helm install external-dns-edgecenter-webhook \
  --namespace external-dns \
  --set externalSecrets.secretStore.name=vault-backend \
  --set externalSecrets.remoteRef.key=external-dns/edgecenter \
  external-dns-edgecenter-webhook/external-dns-edgecenter-webhook
```

### Настройка ExternalDNS для EdgeCenter

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: external-dns-edgecenter
spec:
  template:
    spec:
      containers:
      - name: external-dns
        image: registry.k8s.io/external-dns/external-dns:v0.13.0
        args:
        - --source=service
        - --source=ingress
        - --provider=webhook
        - --webhook-provider-url=http://external-dns-edgecenter-webhook:8888
        - --annotation-filter=external-dns.alpha.kubernetes.io/target-provider=edgecenter
        - --registry=txt
        - --txt-owner-id=external-dns-edgecenter
```

### Настройка ExternalDNS для Yandex

Сначала создайте секрет с учетными данными Yandex Cloud:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: yandex-dns-credentials
  namespace: external-dns
type: Opaque
data:
  folder-id: <base64-encoded-folder-id>
  token: <base64-encoded-token>
```

Затем создайте deployment для Yandex ExternalDNS:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: external-dns-yandex
spec:
  template:
    spec:
      containers:
      - name: external-dns
        image: registry.k8s.io/external-dns/external-dns:v0.13.0
        args:
        - --source=service
        - --source=ingress
        - --provider=yandex
        - --registry=txt
        - --txt-owner-id=external-dns-yandex
        env:
        - name: YANDEX_CLOUD_FOLDER_ID
          valueFrom:
            secretKeyRef:
              name: yandex-dns-credentials
              key: folder-id
        - name: YANDEX_CLOUD_TOKEN
          valueFrom:
            secretKeyRef:
              name: yandex-dns-credentials
              key: token
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

---
# Пример 3: Ingress с EdgeCenter DNS
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: app-edgecenter
  annotations:
    external-dns.alpha.kubernetes.io/target-provider: edgecenter
spec:
  rules:
  - host: app.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: app-edgecenter
            port:
              number: 80

---
# Пример 4: Ingress с Yandex DNS
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: app-yandex
  # Не указываем target-provider
spec:
  rules:
  - host: app.example.ru
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: app-yandex
            port:
              number: 80
```

### Принцип работы

1. EdgeCenter ExternalDNS:
   - Обрабатывает только ресурсы с аннотацией `external-dns.alpha.kubernetes.io/target-provider=edgecenter`
   - Создает DNS-записи в EdgeCenter DNS
   - Использует webhook для взаимодействия с API EdgeCenter

2. Yandex ExternalDNS:
   - Обрабатывает все остальные ресурсы (без специальной аннотации)
   - Создает DNS-записи в Yandex DNS
   - Напрямую взаимодействует с API Yandex Cloud

### Рекомендации

1. Используйте разные `txt-owner-id` для каждого экземпляра ExternalDNS
2. Проверяйте логи обоих экземпляров при отладке
3. При первом развертывании используйте режим `--dry-run` для проверки
4. Следите за правильностью аннотаций в ваших ресурсах
5. Рекомендуется использовать разные домены для разных провайдеров
6. Убедитесь, что у вас настроены правильные разрешения (IAM) для обоих провайдеров
7. Используйте разные неймспейсы для разных провайдеров, если это возможно

### Отладка

Для проверки работы мультипровайдерной конфигурации:

```bash
# Проверка логов EdgeCenter ExternalDNS
kubectl logs -f deployment/external-dns-edgecenter -n external-dns

# Проверка логов Yandex ExternalDNS
kubectl logs -f deployment/external-dns-yandex -n external-dns

# Проверка логов EdgeCenter Webhook
kubectl logs -f deployment/external-dns-edgecenter-webhook -n external-dns

# Проверка созданных DNS-записей в EdgeCenter
curl -H "Authorization: Bearer $TOKEN" https://api.edgecenter.ru/dns/v2/zones/$ZONE_ID/records

# Проверка созданных DNS-записей в Yandex Cloud
yc dns zone list-records --name=$ZONE_NAME
```

### Известные проблемы

1. При использовании нескольких провайдеров убедитесь, что у вас нет конфликтов в TXT-записях для registry
2. Если вы используете разные домены для разных провайдеров, убедитесь, что domain-filter настроен правильно
3. При переключении провайдера для существующей записи может потребоваться ручное удаление старой записи
4. Возможны задержки при обновлении DNS-записей из-за кэширования DNS и TTL
5. При использовании режима `--dry-run` записи не будут созданы, но в логах вы увидите, какие изменения были бы применены

### Устранение неполадок

Если у вас возникли проблемы:

1. Проверьте логи всех компонентов:
   - EdgeCenter ExternalDNS
   - Yandex ExternalDNS
   - EdgeCenter Webhook
   - Kubernetes Events (`kubectl get events`)

2. Убедитесь, что все секреты настроены правильно:
   ```bash
   # Проверка секрета EdgeCenter
   kubectl get secret -n external-dns external-dns-edgecenter-webhook -o yaml
   
   # Проверка секрета Yandex
   kubectl get secret -n external-dns yandex-dns-credentials -o yaml
   ```

3. Проверьте настройки RBAC:
   ```bash
   # Проверка прав ServiceAccount
   kubectl get clusterrole external-dns -o yaml
   kubectl get clusterrolebinding external-dns-viewer -o yaml
   ```

4. Проверьте сетевую доступность:
   ```bash
   # Для EdgeCenter Webhook
   kubectl exec -it deploy/external-dns-edgecenter -n external-dns -- wget -qO- http://external-dns-edgecenter-webhook:8888/health
   
   # Для Yandex API
   kubectl exec -it deploy/external-dns-yandex -n external-dns -- wget -qO- https://api.cloud.yandex.net/dns/v1/zones
   ```

5. Проверьте конфигурацию ресурсов:
   ```bash
   # Проверка аннотаций на сервисах
   kubectl get svc -A -o custom-columns=NAME:.metadata.name,ANNOTATIONS:.metadata.annotations
   
   # Проверка аннотаций на ингрессах
   kubectl get ing -A -o custom-columns=NAME:.metadata.name,ANNOTATIONS:.metadata.annotations
   ``` 