package docker

// VPN is the interface to deploy and connect to a VPN inside the Docker
// private network area.
type VPN interface {
	Deploy() error
	Clean() error
}
