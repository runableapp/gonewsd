package main

import (
	"fmt"
	"gonewsd/internal/auth"
)

func main() {
	tests := []string{
		"../../etc",
		"/etc/passwd",
		"test.utf8",
		"test..group",
		".hidden",
		"a/b",
		"valid.group.name",
		"x;rm -rf /",
	}
	for _, t := range tests {
		fmt.Printf("ValidGroupName(%q) = %v\n", t, auth.ValidGroupName(t))
	}
}
