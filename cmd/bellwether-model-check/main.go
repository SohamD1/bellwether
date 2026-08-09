package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/SohamD1/bellwether/internal/inference"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("bellwether-model-check", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var modelPath string
	flags.StringVar(&modelPath, "model", "", "path to a LightGBM text model")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if modelPath == "" {
		return errors.New("--model is required")
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}

	_, err := inference.Load(modelPath)
	return err
}
