SHELL := /usr/bin/env bash

PROJECT_DIR := .

.DEFAULT_GOAL := all

.PHONY: all test build desktop docker clean

all: test build

test:
	cd "$(PROJECT_DIR)" && bash scripts/test.sh

build:
	cd "$(PROJECT_DIR)" && bash scripts/build.sh --skip-tests

desktop:
	cd "$(PROJECT_DIR)" && bash scripts/build-desktop.sh --skip-tests

docker:
	cd "$(PROJECT_DIR)" && docker build -t playlist-forge:local .

clean:
	rm -rf -- \
		bin coverage coverage.out outputs work \
		"$(PROJECT_DIR)/bin" \
		"$(PROJECT_DIR)/coverage" \
		"$(PROJECT_DIR)/coverage.out" \
		"$(PROJECT_DIR)/outputs" \
		"$(PROJECT_DIR)/work" \
		"$(PROJECT_DIR)/web/coverage" \
		"$(PROJECT_DIR)/web/node_modules" \
		"$(PROJECT_DIR)/internal/webui/dist"
	find . -type f \( \
		-name '*.db-shm' -o \
		-name '*.db-wal' -o \
		-name '.DS_Store' \
	\) -delete
