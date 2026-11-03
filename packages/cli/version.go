package main

import (
	"fmt"
)

var versionAction = &Command{
	Name:  "version",
	Usage: "print the cli version",
	Action: func(c *Context) error {
		fmt.Println(Version)
		return nil
	},
}
