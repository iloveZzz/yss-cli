package scaffold

import "context"

// formalState holds what the approved-contract path derives for the manifest.
type formalState struct{}

func (g *dddGen) validateFormalContract(ctx context.Context) error {
	return fail("正式合同模式尚未在 Go 实现中提供")
}

func (g *dddGen) formalManifest(t *Tree, fingerprint *OMap, generator, ownership *OMap) (*OMap, error) {
	return nil, fail("正式合同模式尚未在 Go 实现中提供")
}

func (g *mvcGen) validateFormalContract(ctx context.Context) error {
	return fail("正式合同模式尚未在 Go 实现中提供")
}

func (g *mvcGen) formalManifest(t *Tree, fingerprint *OMap, generator, ownership *OMap, mainSource string) (*OMap, error) {
	return nil, fail("正式合同模式尚未在 Go 实现中提供")
}
