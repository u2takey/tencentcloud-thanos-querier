

.PHONY: all
all: build

.PHONY: build

build:
	GO111MODULE=on go build -mod=vendor -o ./build/tq ./cmd

build-linux:
	GO111MODULE=on GOOS=linux go build -mod=vendor -o ./build/tq ./cmd

docker: build-linux
	docker build -t tq-thanos ./build

run: build-linux
	 CONTEXT=${PWD}/build && docker-compose -f build/docker-compose.yml build  \
    	&&  docker-compose -f build/docker-compose.yml up