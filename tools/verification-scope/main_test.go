package main

import "testing"

func TestFunctionAndSharedDeclarationChanges(t *testing.T) {
	before := "package p\nconst policy=1\nfunc route() { println(1) }\nfunc other() {}\n"
	c, e := compare(sources{Before: before, After: "package p\nconst policy=1\nfunc route() { println(2) }\nfunc other() {}\n"})
	if e != nil || c.SharedChanged || len(c.Functions) != 1 || c.Functions[0] != "route" {
		t.Fatalf("%#v %v", c, e)
	}
	c, e = compare(sources{Before: before, After: "package p\nconst policy=2\nfunc route() { println(1) }\nfunc other() {}\n"})
	if e != nil || !c.SharedChanged {
		t.Fatalf("shared change missed: %#v %v", c, e)
	}
}

func TestImportSideEffectsAreSharedChanges(t *testing.T) {
	c, err := compare(sources{Before: "package p\nfunc route() {}\n", After: "package p\nimport _ \"net/http/pprof\"\nfunc route() {}\n"})
	if err != nil || !c.SharedChanged {
		t.Fatalf("import side effect missed: %#v %v", c, err)
	}
}
