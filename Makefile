EXAMPLE ?= skipchain

run:
	go run ./examples/${EXAMPLE}/main.go

plot:
	go run ./metrics/plotter/ ${ARGS}

clean:
	killall openvpn || true
	kubectl delete deployments -l go.dedis.ch.app=simnet || true
	kubectl delete service simnet-router || true

build_monitor:
	docker build -t dedis/simnet-monitor -f daemon/monitor/Dockerfile .

build_router:
	docker build -t dedis/simnet-router-init -f daemon/router/Init.Dockerfile .
	docker build -t dedis/simnet-router -f daemon/router/Dockerfile .
