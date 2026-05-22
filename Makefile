.DEFAULT_GOAL=run
BINARY_NAME=wfts
CONFIG_PATH=./configs/default.json
LOCAL_BIN_PATH=.data
IMAGE_NAME=wfts
HOST_PORT=80
MEMORY_LIM=1600m

build: test
	go build -o ./.bin/${BINARY_NAME} ./cmd/app/main.go

test:
	go test ./... -v -count=1 -parallel=1 | grep -v "no test files" || true

run: build
	./.bin/${BINARY_NAME} --config=${CONFIG_PATH}

panic-test: build
	./.bin/${BINARY_NAME} > ./logs/panic.txt 2>&1

clear:
	rm -f ${LOCAL_BIN_PATH}/index/*
	rm -f ${LOCAL_BIN_PATH}/ngs/*
	rm -f ${LOCAL_BIN_PATH}/*.bin
	rm -f ${LOCAL_BIN_PATH}/shingles/*

docker-build:
	docker build -t ${IMAGE_NAME} .

docker-run:
	mkdir -p ${LOCAL_BIN_PATH}
	docker run --user $(id -u):$(id -g) --memory=$(MEMORY_LIM) --memory-swap=$(MEMORY_LIM) -p ${HOST_PORT}:8080 -v ${LOCAL_BIN_PATH}:/app/.data ${IMAGE_NAME}

cleard:
	rm -rf ${LOCAL_BIN_PATH}