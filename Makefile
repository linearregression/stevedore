# Old-skool build tools.
#
# Targets (see each target for more information):
#   build/container:	builds the Docker image used to compile all golang code
#   build:        		builds binaries, placing each next to it's respective main pkg
#   test: 		    		runs tests
#   lint:    				  lints the source tree
#   install: 					builds, tests, then copies the resulting binary to $GOPATH/bin/
#   dockerize:    		builds, tests, then makes a Docker image for each binary
#	  clean:			  		removes build artifacts (aka binaries)
#	  clean-all:	  		cleans, then removes the artifact for build/container

SHELL := /bin/bash

# some terminal color escape codes
LIGHT_GREEN := $(shell echo -e "\033[1;32m")
NC := $(shell echo -e "\033[0m") # No Color

# obtains the latest git SHA for the current repo, which is used to tag docker
# images and set properties in the golang binaries
GIT_SHA:= $(shell git rev-parse HEAD 2>/dev/null | cut -c 1-7)

# Platform/arch specific crud:
# - On linux, run the container with the current uid, so files produced from
#   within the container are owned by the current user, rather than root.
# - On OSX, don't do anything with the container user, and let boot2docker manage
#   permissions on the /Users mount that it sets up
# - cross-compile (if necessary) in the container, but keep the same binary name
DOCKER_USER := $(shell if [[ "$$OSTYPE" != "darwin"* ]]; then USER_ARG="--user=`id -u`"; fi; echo "$$USER_ARG")
GOOS   := $(shell if [[ "$$OSTYPE" == "darwin"* ]]; then echo "darwin"; else echo "linux"; fi)
GOARCH := $(shell if [[ `uname -a` == *"x86_64"* ]]; then echo "amd64"; else echo "386"; fi)

# the "root" pkg contained in this project.  If the project contains multiple
# binaries, each binary's main pkg will be in a subdir of SRC_ROOT
SRC_ROOT=github.com/zulily/stevedore/

.DEFAULT_GOAL := build

# Builds the docker image that we'll use to compile all subsequent golang code
# touch: http://www.gnu.org/software/make/manual/make.html#Empty-Targets
build/container: build/Dockerfile
	@echo "${LIGHT_GREEN}building Docker image: boilerplate/zulily-stevedore-compile...${NC}"
	@docker build --no-cache -t boilerplate/zulily-stevedore-compile build/ > /dev/null
	touch $@

clean:
	rm -f stevedore

clean-all: clean
	rm -f build/container

# runs a `godep save` in a container, outputting the results via the volume mount
godep: build/container
	@docker run --rm \
		-v "$$PWD":"/go/src/${SRC_ROOT}" \
		-w "/go/src/${SRC_ROOT}" \
		${DOCKER_USER} \
	  -t boilerplate/zulily-stevedore-compile \
		godep save
.PHONY: godep

STEVEDORE_SRCS = $(shell find . -type f -name '*.go')

# builds the binary in a Docker container and copies it to a volume mount (/output/)
stevedore: $(STEVEDORE_SRCS) build/container
	@echo "${LIGHT_GREEN}building ${GOOS}-compatible ${GOARCH} binary for stevedore...${NC}"
	@docker run --rm \
		-v "$$PWD":"/go/src/${SRC_ROOT}" \
		-w "/go/src/${SRC_ROOT}" \
		-v "$${PWD}/":/output \
		${DOCKER_USER} \
		-e "GOOS=${GOOS}" \
	  -e "GOARCH=${GOARCH}" \
		-e "BINARY=stevedore" \
		-e "GIT_SHA=${GIT_SHA}" \
		-t boilerplate/zulily-stevedore-compile

build: stevedore

# runs any tests inside a Docker container
test: build
	@echo "${LIGHT_GREEN}running tests for stevedore...${NC}"
	@docker run --rm \
		-v "$$PWD":"/go/src/${SRC_ROOT}" \
		-w "/go/src/${SRC_ROOT}" \
		boilerplate/zulily-stevedore-compile \
		godep go test -v ./...
.PHONY: test

install: build test
	@echo "${LIGHT_GREEN}copying binary to ${GOPATH}/bin/...${NC}"
	cp "$$PWD"/stevedore $${GOPATH}/bin/
.PHONY: install

# lints the entire src tree inside a Docker container, using golint
lint: build/container
	@echo "${LIGHT_GREEN}linting code...${NC}"
	@docker run --rm \
		-v "$$PWD":"/go/${SRC_ROOT}" \
		-w "/go/${SRC_ROOT}/" \
		boilerplate/zulily-stevedore-compile \
		golint ./...
.PHONY: lint

# Build a linux-compatible binary and a docker image that uses the binary as it's entrypoint
dockerize: build/container
	@echo "${LIGHT_GREEN}building stevedore binary for inclusion in Docker image...${NC}"
	@docker run --rm \
		-v "$$PWD":"/go/src/${SRC_ROOT}" \
		-w "/go/src/${SRC_ROOT}" \
		-v "$${PWD}/":/output \
		${DOCKER_USER} \
		-e "BINARY=stevedore" \
		-e "GIT_SHA=${GIT_SHA}" \
		-t boilerplate/zulily-stevedore-compile

	@echo "${LIGHT_GREEN}building Docker image 'zulily/stevedore:${GIT_SHA}'...${NC}"
	@docker build --no-cache \
		-t "zulily/stevedore:${GIT_SHA}" ${PWD}
.PHONY: dockerize

