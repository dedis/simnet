run:
	go run ./examples/status.go

clean:
	kubectl delete deployment simnet

build_monitor:
	docker build -t dedis/simnet-monitor -f monitor/Dockerfile .

run_monitor:
	docker run -d -v /var/run/docker.sock:/var/run/docker.sock dedis/simnet-monitor
