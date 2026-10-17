# default action for make is to run the application
SHELL := /bin/zsh

PROJECT_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
AWS_ENV_FILE ?= $(PROJECT_DIR).aws.env
GO ?= $(HOME)/.gvm/gos/go1.26.0/bin/go
AWS_REGION ?= us-east-1

.PHONY: all
all: run

.PHONY: run
run:
	. "$(AWS_ENV_FILE)" && cd "$(PROJECT_DIR)" && aws dynamodb delete-table --table-name instant-payments-go --region $(AWS_REGION) >/dev/null 2>&1 || true
	. "$(AWS_ENV_FILE)" && cd "$(PROJECT_DIR)" && aws dynamodb wait table-not-exists --table-name instant-payments-go --region $(AWS_REGION)
	. "$(AWS_ENV_FILE)" && cd "$(PROJECT_DIR)" && AWS_REGION=$(AWS_REGION) $(GO) run ./cmd/main

.PHONY: ijhttp
ijhttp:
	ijhttp instant-payments-verification.http --env-file=http-client.env.json --env=dev
