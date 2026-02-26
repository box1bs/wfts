BINARY_NAME=wfts
.DEFAULT_GOAL=index
CONFIG_PATH=./configs/app_config.json

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