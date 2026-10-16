// 对拍专用驱动：直接调用 internal/scaffold.Run，覆盖 CLI 不暴露的开关（独立模式的参考示例）。
// 仅供 tools/scaffold-parity 使用，不进入发行包，也不是运行依赖。
//
//	go run ./tools/scaffold-parity/driver --options options.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/iloveZzz/yss-cli/internal/scaffold"
)

type input struct {
	Architecture         string `json:"architecture"`
	ProjectName          string `json:"projectName"`
	BasePackage          string `json:"basePackage"`
	OutputDir            string `json:"outputDir"`
	PlatformProfile      string `json:"platformProfile"`
	SpringBootVersion    string `json:"springBootVersion"`
	JavaVersion          string `json:"javaVersion"`
	GroupID              string `json:"groupId"`
	ProjectVersion       string `json:"projectVersion"`
	ParentGroupID        string `json:"parentGroupId"`
	ParentArtifactID     string `json:"parentArtifactId"`
	ParentVersion        string `json:"parentVersion"`
	YssComponentsVersion string `json:"yssComponentsVersion"`
	ContractFile         string `json:"contractFile"`
	ContractID           string `json:"contractId"`
	ContractVersion      string `json:"contractVersion"`
	ApprovalRef          string `json:"approvalRef"`
	CompilerDraftRef     string `json:"compilerDraftRef"`
	PersistedRef         string `json:"persistedRef"`
	WithExample          *bool  `json:"withExample"`
}

func main() {
	path := flag.String("options", "", "JSON 选项文件")
	flag.Parse()
	data, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var in input
	if err = json.Unmarshal(data, &in); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	result, err := scaffold.Run(context.Background(), scaffold.Options{
		Architecture: in.Architecture, ProjectName: in.ProjectName, BasePackage: in.BasePackage, OutputDir: in.OutputDir,
		PlatformProfile: in.PlatformProfile, SpringBootVersion: in.SpringBootVersion, JavaVersion: in.JavaVersion,
		GroupID: in.GroupID, ProjectVersion: in.ProjectVersion, ParentGroupID: in.ParentGroupID, ParentArtifactID: in.ParentArtifactID,
		ParentVersion: in.ParentVersion, YssComponentsVersion: in.YssComponentsVersion,
		ContractFile: in.ContractFile, ContractID: in.ContractID, ContractVersion: in.ContractVersion,
		ApprovalRef: in.ApprovalRef, CompilerDraftRef: in.CompilerDraftRef, PersistedRef: in.PersistedRef,
		WithExample: in.WithExample,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("{\"projectRoot\":%q}\n", result.ProjectRoot)
}
