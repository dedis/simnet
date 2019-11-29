run:
	go run ./examples/status.go

plot:
	go run ./visu/mod.go ${ARGS}

clean:
	kubectl delete deployments -l app=simnet

build_monitor:
	docker build -t dedis/simnet-monitor -f monitor/Dockerfile .

build_router:
	docker build -t dedis/simnet-router -f router/Dockerfile .
