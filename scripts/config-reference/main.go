package main

import (
	"flag"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"ecs/internal/config"
)

var environmentPattern = regexp.MustCompile(`os\.Getenv\("([A-Z][A-Z0-9_]*)"\)`)

func main() {
	documentPath := flag.String("document", "docs/configuration-reference.md", "configuration reference")
	configSourcePath := flag.String("source", "internal/config/config.go", "configuration source")
	list := flag.Bool("list", false, "list required configuration paths")
	flag.Parse()

	paths := configPaths(reflect.TypeOf(config.Config{}), "")
	if *list {
		for _, path := range paths {
			fmt.Println(path)
		}
		return
	}
	document, err := os.ReadFile(*documentPath)
	if err != nil {
		fatal(err)
	}
	source, err := os.ReadFile(*configSourcePath)
	if err != nil {
		fatal(err)
	}
	text := string(document)
	var missing []string
	for _, path := range paths {
		if !strings.Contains(text, "`"+path+"`") {
			missing = append(missing, path)
		}
	}
	environments := environmentPattern.FindAllStringSubmatch(string(source), -1)
	for _, match := range environments {
		if !strings.Contains(text, "`"+match[1]+"`") {
			missing = append(missing, "environment "+match[1])
		}
	}
	if len(missing) > 0 {
		fmt.Fprintln(os.Stderr, "configuration reference is incomplete:")
		for _, value := range missing {
			fmt.Fprintln(os.Stderr, " -", value)
		}
		os.Exit(1)
	}
	fmt.Printf("Configuration reference covers %d fields and %d fixed environment variables.\n", len(paths), len(environments))
}

func configPaths(value reflect.Type, prefix string) []string {
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	var paths []string
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		paths = append(paths, expand(field.Type, path)...)
	}
	sort.Strings(paths)
	return paths
}

func expand(value reflect.Type, path string) []string {
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Struct:
		return configPaths(value, path)
	case reflect.Slice, reflect.Array:
		element := value.Elem()
		if element.Kind() == reflect.Struct {
			return configPaths(element, path+"[]")
		}
	case reflect.Map:
		element := value.Elem()
		if element.Kind() == reflect.Struct {
			return configPaths(element, path+".<name>")
		}
		if element.Kind() == reflect.Slice && element.Elem().Kind() == reflect.Struct {
			return configPaths(element.Elem(), path+".<name>[]")
		}
	}
	return []string{path}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
