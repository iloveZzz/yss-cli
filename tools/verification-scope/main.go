// verification-scope compares Go declarations so focused checks cannot hide shared changes.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"sort"
)

type sources struct{ Before, After string }
type changes struct {
	Functions     []string
	SharedChanged bool
}

func declarations(source string) (map[string]string, string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "input.go", source, 0)
	if err != nil {
		return nil, "", err
	}
	functions := map[string]string{}
	var shared bytes.Buffer
	for _, decl := range file.Decls {
		var out bytes.Buffer
		if err = format.Node(&out, fset, decl); err != nil {
			return nil, "", err
		}
		if fn, ok := decl.(*ast.FuncDecl); ok {
			name := fn.Name.Name
			if fn.Recv != nil {
				var recv bytes.Buffer
				format.Node(&recv, fset, fn.Recv)
				name = recv.String() + "." + name
			}
			functions[name] = out.String()
		} else {
			shared.Write(out.Bytes())
		}
	}
	return functions, shared.String(), nil
}
func compare(input sources) (changes, error) {
	before, oldShared, err := declarations(input.Before)
	if err != nil {
		return changes{}, err
	}
	after, newShared, err := declarations(input.After)
	if err != nil {
		return changes{}, err
	}
	result := changes{Functions: []string{}, SharedChanged: oldShared != newShared}
	names := map[string]bool{}
	for name := range before {
		names[name] = true
	}
	for name := range after {
		names[name] = true
	}
	for name := range names {
		if before[name] != after[name] {
			result.Functions = append(result.Functions, name)
		}
	}
	sort.Strings(result.Functions)
	return result, nil
}
func main() {
	var inputs map[string]sources
	if err := json.NewDecoder(os.Stdin).Decode(&inputs); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	result := map[string]changes{}
	for file, input := range inputs {
		change, err := compare(input)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		result[file] = change
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
