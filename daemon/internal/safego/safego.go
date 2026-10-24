package safego

import (
	"log"
	"runtime/debug"
)

func Go(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in goroutine %s: %v\n%s", name, r, debug.Stack())
			}
		}()
		fn()
	}()
}

func Tick(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in tick %s: %v\n%s", name, r, debug.Stack())
		}
	}()
	fn()
}
