// A read-only source / new-output-only cache correctness and capacity fixture.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/Kubonsang/testplay-runner/internal/contentcache"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	first := flag.String("first", "", "first existing Library cache")
	second := flag.String("second", "", "second existing Library cache")
	output := flag.String("output", "", "new output directory")
	flag.Parse()
	if !filepath.IsAbs(*output) || !filepath.IsAbs(*first) || !filepath.IsAbs(*second) {
		panic("absolute paths required")
	}
	if err := os.Mkdir(*output, 0700); err != nil {
		panic(err)
	}
	store := contentcache.New(filepath.Join(*output, "store"))
	ctx := context.Background()
	key := strings.Repeat("a", 64)
	records := []map[string]any{}
	for i, source := range []string{*first, *second} {
		project := filepath.Join(*output, fmt.Sprintf("project-%d", i))
		if err := os.Mkdir(project, 0700); err != nil {
			panic(err)
		}
		started := time.Now()
		if err := store.Publish(ctx, project, key, source); err != nil {
			panic(err)
		}
		publish := time.Since(started).Milliseconds()
		started = time.Now()
		hit, err := store.Restore(ctx, project, key, filepath.Join(project, "Library"))
		if err != nil || !hit {
			panic(fmt.Sprintf("restore: %v", err))
		}
		records = append(records, map[string]any{"source": source, "publishMs": publish, "restoreMs": time.Since(started).Milliseconds(), "verified": true})
	}
	usage, err := store.Inspect(ctx, false)
	if err != nil {
		panic(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"usage": usage, "copies": records}); err != nil {
		panic(err)
	}
}
