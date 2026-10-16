package scaffold

import "strings"

// parentPomTemplate is the layered MVC parent pom with @name@ markers where the
// generator interpolates inputs.
const parentPomTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:schemaLocation="http://maven.apache.org/POM/4.0.0 https://maven.apache.org/xsd/maven-4.0.0.xsd">
  <modelVersion>4.0.0</modelVersion>
  <parent><groupId>@PG@</groupId><artifactId>@PA@</artifactId><version>@PVR@</version><relativePath/></parent>
  <groupId>@G@</groupId><artifactId>@PN@</artifactId><version>@PV@</version><packaging>pom</packaging>
  <properties><java.version>@java_version@</java.version><spring-boot.version>@boot_version@</spring-boot.version><maven.compiler.source>@java_version@</maven.compiler.source><maven.compiler.target>@java_version@</maven.compiler.target><project.build.sourceEncoding>UTF-8</project.build.sourceEncoding><yss-components.version>@YC@</yss-components.version></properties>
  <modules><module>@PN@-@MODS@</module></modules>
  <dependencyManagement><dependencies><dependency><groupId>org.springframework.boot</groupId><artifactId>spring-boot-dependencies</artifactId><version>@boot_version@</version><scope>import</scope><type>pom</type></dependency><dependency><groupId>com.yss.cloud</groupId><artifactId>yss-components-bom</artifactId><version>${yss-components.version}</version><scope>import</scope><type>pom</type></dependency></dependencies></dependencyManagement>
  <build><plugins><plugin><groupId>org.springframework.boot</groupId><artifactId>spring-boot-maven-plugin</artifactId><version>@boot_version@</version><configuration><skip>true</skip></configuration></plugin><plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-enforcer-plugin</artifactId><version>@enforcer_version@</version><executions><execution><id>enforce-platform</id><goals><goal>enforce</goal></goals><configuration><rules><requireJavaVersion><version>@java_range@</version></requireJavaVersion><requireMavenVersion><version>[3.6.3,)</version></requireMavenVersion><dependencyConvergence/></rules></configuration></execution></executions></plugin></plugins><pluginManagement><plugins><plugin><groupId>org.springframework.boot</groupId><artifactId>spring-boot-maven-plugin</artifactId><version>@boot_version@</version><executions><execution><goals><goal>repackage</goal></goals></execution></executions></plugin><plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-surefire-plugin</artifactId><version>@surefire_version@</version></plugin><plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-compiler-plugin</artifactId><version>@compiler_version@</version><configuration><source>@java_version@</source><target>@java_version@</target><parameters>true</parameters><annotationProcessorPaths><path><groupId>org.projectlombok</groupId><artifactId>lombok</artifactId><version>@lombok_version@</version></path><path><groupId>org.mapstruct</groupId><artifactId>mapstruct-processor</artifactId><version>@mapstruct_version@</version></path><path><groupId>org.projectlombok</groupId><artifactId>lombok-mapstruct-binding</artifactId><version>@lombok_mapstruct_binding_version@</version></path></annotationProcessorPaths></configuration></plugin></plugins></pluginManagement></build>
</project>`

func parentPom(name string, coordinates *OMap, modules []string, platform *OMap) string {
	vars := platformTemplateVars(platform)
	parent := asMap(mustGet(coordinates, "parent"))
	var list strings.Builder
	for _, module := range modules {
		list.WriteString("<module>" + name + "-" + module + "</module>")
	}
	text := strings.Replace(parentPomTemplate, "<module>@PN@-@MODS@</module>", list.String(), 1)
	pairs := []string{
		"@PG@", xmlEscape(jsString(mustGet(parent, "group_id"))),
		"@PA@", xmlEscape(jsString(mustGet(parent, "artifact_id"))),
		"@PVR@", xmlEscape(jsString(mustGet(parent, "version"))),
		"@G@", xmlEscape(jsString(mustGet(coordinates, "group_id"))),
		"@PN@", xmlEscape(name),
		"@PV@", xmlEscape(jsString(mustGet(coordinates, "project_version"))),
		"@YC@", xmlEscape(jsString(mustGet(coordinates, "yss_components_version"))),
	}
	for _, key := range vars.Keys() {
		pairs = append(pairs, "@"+key+"@", jsString(mustGet(vars, key)))
	}
	return strings.NewReplacer(pairs...).Replace(text)
}
