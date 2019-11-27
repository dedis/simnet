run:
	go run ./examples/status.go

clean:
	kubectl delete deployment simnet
