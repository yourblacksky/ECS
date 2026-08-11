//go:build !windows

package servicehost

func IsService() bool {
	return false
}

func Run(string, func(<-chan struct{}, <-chan struct{}) error) error {
	return nil
}
