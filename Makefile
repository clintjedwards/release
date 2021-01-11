APP_NAME = release
BUILD_PATH = /tmp/${APP_NAME}
EPOCH_TIME = $(shell date +%s)
GIT_COMMIT = $(shell git rev-parse --short HEAD)
SEMVER = 0.0.1
SHELL = /bin/bash
VERSION = ${SEMVER}_${EPOCH_TIME}_${GIT_COMMIT}


## help: prints this help message
help:
	@echo "Usage: "
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' |  sed -e 's/^/ /'

## build: run tests and compile full app
build:
	go mod tidy
	go build -o $(BUILD_PATH)

## install: build application and install on system
install: build
	sudo mv $(BUILD_PATH) /usr/local/bin/
	sudo chmod +x /usr/local/bin/${APP_NAME}

## release: release to github
release: check-version-included build
	release clintjedwards/release ${version} -b $(BUILD_PATH)

check-version-included:
ifndef version
	$(error version is undefined; ex. make release version=1.0.0)
endif
