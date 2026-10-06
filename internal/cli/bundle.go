package cli

import (
	"context"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"strings"
)

func bundleCommand(ctx context.Context, o options) (any, string, error) {
	p := o.values["profile"]
	if len(o.args) != 2 || (o.args[1] != "inspect" && o.args[1] != "export") {
		return nil, p, domain.Fail("ARGUMENT", "bundle supports inspect or export")
	}
	for k := range o.values {
		switch k {
		case "profile", "json", "help", "out":
		default:
			return nil, p, domain.Fail("ARGUMENT", "bundle does not support --"+k)
		}
	}
	if _, e := domain.GetProfile(p); e != nil {
		return nil, p, e
	}
	if o.args[1] == "inspect" {
		if _, ok := o.values["out"]; ok {
			return nil, p, domain.Fail("ARGUMENT", "bundle inspect does not write output")
		}
		r, e := bundle.Inspect(p)
		return r, p, e
	}
	if strings.TrimSpace(o.values["out"]) == "" {
		return nil, p, domain.Fail("ARGUMENT", "bundle export requires --out <new directory>")
	}
	r, e := bundle.Export(ctx, p, o.values["out"])
	return r, p, e
}
