package kernel

func GuidanceContractRef(profile string) string {
	skill := "yss-product-lifecycle"
	if profile == "design" {
		skill = "yss-strategic-design"
	}
	if profile == "backend" || profile == "frontend" {
		skill = "harness-orchestrator"
	}
	return ".agents/skills/" + skill + "/references/orchestration-contract.yaml"
}
