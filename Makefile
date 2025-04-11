.PHONY: all build test clean

all: build

build:
	go build -o webhook .

test:
	go test -v ./...

clean:
	rm -f webhook

docker-build:
	docker build -t external-dns-edgecenter-webhook:latest .

docker-push:
	docker push external-dns-edgecenter-webhook:latest 