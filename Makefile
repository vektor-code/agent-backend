# Language agents the operator injects into application pods.
# Always linux/amd64 so cluster nodes can run the operator init copy.
#
#   docker login registry.ext.cloudraft.net:18001
#   make agents-push REGISTRY=registry.ext.cloudraft.net:18001/development/code/crnet-apm

REGISTRY ?= registry.int.cloudraft.net:18001/development/code/crnet-apm
TAG ?= dev
PLATFORM ?= linux/amd64

.PHONY: agents agent-java agent-python agent-nodejs agents-push

agents: agent-java agent-python agent-nodejs

agent-java:
	docker build --platform $(PLATFORM) -f Dockerfile.agent-java -t $(REGISTRY)/instrumentation-java:$(TAG) .

agent-python:
	docker build --platform $(PLATFORM) -f Dockerfile.agent-python -t $(REGISTRY)/instrumentation-python:$(TAG) .

agent-nodejs:
	docker build --platform $(PLATFORM) -f Dockerfile.agent-nodejs -t $(REGISTRY)/instrumentation-nodejs:$(TAG) .

agents-push: agents
	docker push $(REGISTRY)/instrumentation-java:$(TAG)
	docker push $(REGISTRY)/instrumentation-python:$(TAG)
	docker push $(REGISTRY)/instrumentation-nodejs:$(TAG)
