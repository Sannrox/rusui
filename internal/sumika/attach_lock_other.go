//go:build !darwin && !linux

package sumika

import "fmt"

func LockAttach() (func(), error) {
	return nil, fmt.Errorf("Sumika Attach is unsupported on this platform")
}
