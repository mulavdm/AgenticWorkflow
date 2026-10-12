package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	args := os.Args
	fmt.Println("Mock FFmpeg started with args:", args)

	if len(args) == 0 {
		return
	}

	lastArg := args[len(args)-1]

	// If the last arg ends with .mp4, just create a dummy file
	if filepath.Ext(lastArg) == ".mp4" {
		dir := filepath.Dir(lastArg)
		os.MkdirAll(dir, 0755)
		os.WriteFile(lastArg, []byte("dummy video content"), 0644)
		return
	}

	// If the last arg ends with .jpg, assume it's a pattern
	if filepath.Ext(lastArg) == ".jpg" {
		dir := filepath.Dir(lastArg)
		os.MkdirAll(dir, 0755)
		
		f1 := filepath.Join(dir, "frame_0001.jpg")
		f2 := filepath.Join(dir, "frame_0002.jpg")
		
		os.WriteFile(f1, []byte("dummy image 1"), 0644)
		os.WriteFile(f2, []byte("dummy image 2"), 0644)
	}
}
