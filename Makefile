SHELL := /usr/bin/env bash

PROJECT_DIR := .

.DEFAULT_GOAL := all

.PHONY: all test build desktop clean

all: test build

test:
	cd "$(PROJECT_DIR)" && bash scripts/test.sh

build:
	cd "$(PROJECT_DIR)" && bash scripts/build-desktop.sh --skip-tests

desktop: build

clean:
	rm -rf -- \
		coverage coverage.out work \
		"$(PROJECT_DIR)/coverage" \
		"$(PROJECT_DIR)/coverage.out" \
		"$(PROJECT_DIR)/work" \
		"$(PROJECT_DIR)/web/coverage" \
		"$(PROJECT_DIR)/web/node_modules" \
		"$(PROJECT_DIR)/internal/webui/dist"
	find . -type f \( \
		-name '*.db-shm' -o \
		-name '*.db-wal' -o \
		-name '.DS_Store' \
	\) -delete
