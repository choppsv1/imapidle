//
// -*- coding: utf-8 -*-
//
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	// Load the file content
	vFileData, _ := os.ReadFile("version.txt")

	// Convert from Byte array to string and split
	// on newlines. We now have a slice of strings
	vLines := strings.Split(string(vFileData), "\n")

	// Generate a timestamp.
	bTime := time.Now().Format("20060102-1504")

	// Load the count from the 3rd line of the file
	// It's a string so we need to convert to integer
	// Then increment it by 1
	bNum, _ := strconv.Atoi(vLines[2])
	bNum++

	// Generate a single string to write back to the file
	// Note, we didn't change the version string
	outStr := vLines[0] + "\n" + bTime + "\n" + fmt.Sprint(bNum)

	// Write the data back to the file.
	_ = os.WriteFile("version.txt", []byte(outStr), 0777)
}
