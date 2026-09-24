//go:build !darwin && !linux

package sumika

import "fmt"

func sameUserSocket(path string) error {
	return fmt.Errorf("sumika local runtime is unsupported on this platform: %s", path)
}
