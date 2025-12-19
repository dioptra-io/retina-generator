package main

import (
	"fmt"
	"time"
)

func main() {
	cc := make([]int, 1, 1)

	for {
		fmt.Printf("len: %d, cap: %d\n", len(cc), cap(cc))
		cc = append(cc, 1)
		time.Sleep(time.Millisecond * 50)
	}
}
