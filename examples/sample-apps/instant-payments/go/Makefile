SHELL := /bin/sh
PROJECT_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))

.PHONY: all build test coverage run dynamodb-start dynamodb-stop ijhttp
all: build

build:
	cd "$(PROJECT_DIR)" && scripts/build-app.sh

test:
	cd "$(PROJECT_DIR)" && scripts/run-tests.sh

coverage:
	cd "$(PROJECT_DIR)" && scripts/run-tests.sh --coverage

run:
	cd "$(PROJECT_DIR)" && scripts/run-app-local.sh

dynamodb-start:
	cd "$(PROJECT_DIR)" && scripts/start-dynamodb-local.sh

dynamodb-stop:
	cd "$(PROJECT_DIR)" && scripts/stop-dynamodb-local.sh

ijhttp:
	cd "$(PROJECT_DIR)" && ijhttp instant-payments-verification.http --env-file=http-client.env.json --env=dev
