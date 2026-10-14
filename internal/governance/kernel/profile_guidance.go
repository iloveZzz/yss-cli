package kernel

func GuidanceContractRef(profile string) string {
	skill := "yss-product-lifecycle"
	if profile == "design" {
		skill = "yss-strategic-design"
	}
	if profile == "backend" {
		skill = "harness-orchestrator"
	}
	return ".agents/skills/" + skill + "/references/orchestration-contract.yaml"
}
