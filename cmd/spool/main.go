package main

import (
	"context"
	"os"

	"github.com/grintzdel/spool/internal/adapter/cli"
)

var version = "dev"

func main() {
	os.Exit(cli.Execute(context.Background(), version))
}
