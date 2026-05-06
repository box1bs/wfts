.DEFAULT_GOAL=run
BINARY_NAME=wfts
CONFIG_PATH=./configs/default.json
LOCAL_BIN_PATH=.data/
IMAGE_NAME=wfts
HOST_PORT=80

build: test
	go build -o ./.bin/${BINARY_NAME} ./cmd/app/main.go

test:
	go test ./... -v -count=1 -parallel=1 | grep -v "no test files" || true

run: build
	./.bin/${BINARY_NAME} --config=${CONFIG_PATH}

panic-test: build
	./.bin/${BINARY_NAME} > ./logs/panic.txt 2>&1

clear:
	rm -f ${LOCAL_BIN_PATH}index/*
	rm -f ${LOCAL_BIN_PATH}ngs/*
	rm -f ${LOCAL_BIN_PATH}*.bin
	rm -f ${LOCAL_BIN_PATH}shingles/*

docker-build:
	docker build -t ${IMAGE_NAME} .

docker-run:
	docker run --user $(id -u):$(id -g) -p ${HOST_PORT}:8080 -v ${LOCAL_BIN_PATH}:/app/.data ${IMAGE_NAME}

cleard:
	rm -rf ${LOCAL_BIN_PATH}