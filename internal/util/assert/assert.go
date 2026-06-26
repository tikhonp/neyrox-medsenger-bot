// Package assert contains simple assertion helpers used for unrecoverable startup errors.
package assert

import "fmt"

// C panics with msg if condition is false.
func C(condition bool, msg string) {
	if !condition {
		panic(msg)
	}
}

// NoErr panics if err is not nil.
func NoErr(err error, comments ...any) {
	if err != nil {
		if len(comments) > 0 {
			fmt.Println(comments...)
		}
		panic(err)
	}
}
