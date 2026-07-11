package main

import (
	"fmt"
	"os"

	"ncepupan/hdd/internal/filter"
)

// cmdRuleList prints all active filter rules.
func cmdRuleList() error {
	rules := filter.DefaultExcludes()
	for _, r := range rules {
		switch r.Type {
		case filter.RuleName:
			fmt.Printf("name  %s\n", r.Pattern)
		case filter.RulePath:
			fmt.Printf("path  %s\n", r.Pattern)
		case filter.RuleSize:
			fmt.Printf("size  > %d\n", r.MaxSize)
		}
	}
	return nil
}

// cmdRuleTest checks whether a path would be excluded.
func cmdRuleTest(args []string) error {
	if len(args) != 1 {
		return usageError("rule test <path>")
	}
	f := filter.New(filter.DefaultExcludes())
	if f.ExcludePath(args[0]) {
		fmt.Printf("EXCLUDED  %s\n", args[0])
	} else {
		fmt.Printf("INCLUDED  %s\n", args[0])
	}
	return nil
}

// cmdRuleTestWithInfo checks using file info (for size rules).
func cmdRuleTestWithInfo(args []string) error {
	if len(args) != 1 {
		return usageError("rule test <path>")
	}
	path := args[0]
	f := filter.New(filter.DefaultExcludes())

	info, err := os.Stat(path)
	if err != nil {
		// File doesn't exist: test by path only.
		if f.ExcludePath(path) {
			fmt.Printf("EXCLUDED  %s (by name)\n", path)
		} else {
			fmt.Printf("INCLUDED  %s (file not found)\n", path)
		}
		return nil
	}

	if f.Exclude(path, info) {
		fmt.Printf("EXCLUDED  %s\n", path)
	} else {
		fmt.Printf("INCLUDED  %s\n", path)
	}
	return nil
}
