EXAMPLE ?= skipchain

run:
	cd ./examples/${EXAMPLE} && go run main.go ${ARGS}

clean:
	cd ./examples/${EXAMPLE} && go run main.go --do-clean

build_monitor:
	docker build -t dedis/simnet-monitor -f daemon/monitor/Dockerfile .

build_router:
	docker build -t dedis/simnet-router-init -f daemon/router/Init.Dockerfile .
	docker build -t dedis/simnet-router -f daemon/router/Dockerfile .
