package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
)

func main() {
	reg := registry.New()
	idx, err := index.Open("")
	if err != nil {
		fmt.Println("index open:", err)
		os.Exit(1)
	}
	defer idx.Close()

	indexCounts, _ := idx.CountByProvider()
	fmt.Printf("%-14s %-10s %-10s %-8s %s\n", "provider", "discover", "indexed", "dup_ids", "status")
	fmt.Println(strings.Repeat("-", 62))

	var problems int
	for _, p := range reg.All() {
		if !p.Installed() {
			continue
		}
		sums, err := p.Discover(context.Background(), provider.DiscoverOpts{})
		disc := len(sums)
		ind := indexCounts[p.ID()]
		dup := duplicateIDs(sums)
		unique := disc - dup
		status := "ok"
		if err != nil {
			status = "ERR: " + err.Error()
			problems++
		} else if ind < unique {
			status = fmt.Sprintf("STALE (indexed %d, expect %d)", ind, unique)
			problems++
		} else if ind != disc {
			status = fmt.Sprintf("collapsed %d duplicate ids", dup)
		}
		fmt.Printf("%-14s %-10d %-10d %-8d %s\n", p.ID(), disc, ind, dup, status)
	}
	if problems > 0 {
		os.Exit(1)
	}
}

func duplicateIDs(sums []model.Summary) int {
	seen := map[string]int{}
	for _, sm := range sums {
		seen[sm.ID]++
	}
	dup := 0
	for _, n := range seen {
		if n > 1 {
			dup += n - 1
		}
	}
	return dup
}
