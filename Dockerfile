FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o webhook .

FROM alpine:3.19

WORKDIR /app
COPY --from=builder /app/webhook .

EXPOSE 8888
ENTRYPOINT ["/app/webhook"] 