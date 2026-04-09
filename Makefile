.DEFAULT_GOAL=index
BINARY_NAME=wfts
CONFIG_PATH=./configs/default.json
LOCAL_BIN_PATH=./.data/

build: test
	go build -o ./.bin/${BINARY_NAME} ./cmd/app/main.go

test:
	go test ./... -v -count=1 -parallel=1 | grep -v "no test files" || true

index: build
	./.bin/${BINARY_NAME} --config=${CONFIG_PATH}

panic-test: build
	./.bin/${BINARY_NAME} > ./logs/panic.txt 2>&1

index-gui: build
	./.bin/${BINARY_NAME} --gui --config=${CONFIG_PATH}

search: build
	./.bin/${BINARY_NAME} -i --config=${CONFIG_PATH}

clear:
	rm -f ${LOCAL_BIN_PATH}index/*
	rm -f ${LOCAL_BIN_PATH}ngs/*
	rm -f ${LOCAL_BIN_PATH}*.bin
	rm -f ${LOCAL_BIN_PATH}shingles/*

cleard:
	rm -rf .data