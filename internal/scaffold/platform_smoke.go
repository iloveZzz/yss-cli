package scaffold

import "strings"

// platformSmokeTest renders PlatformIntegrationTest.java exactly like the Node
// generators' platformSmokeTest(basePackage, profile, capabilities).
func platformSmokeTest(basePackage string, profile *OMap, capabilities []string) string {
	mapper, simple := "com.fasterxml.jackson.databind.ObjectMapper", "ObjectMapper"
	if intField(profile, "jackson_major") == 3 {
		mapper, simple = "tools.jackson.databind.json.JsonMapper", "JsonMapper"
	}
	feign := ""
	for _, capability := range capabilities {
		if capability == "feign-client" {
			class := "FeignClientFactory"
			if intField(profile, "java_version") == 8 {
				class = "FeignContext"
			}
			feign = strings.ReplaceAll(smokeFeignBlock, "@@FEIGNCLASS@@", class)
		}
	}
	return strings.NewReplacer(
		"@@PKG@@", basePackage,
		"@@NS@@", stringField(profile, "validation_namespace"),
		"@@MAPPERSIMPLE@@", simple,
		"@@MAPPER@@", mapper,
		"@@FEIGN@@", feign,
	).Replace(smokeTemplate)
}

const smokeTemplate = `package @@PKG@@;

import @@NS@@.validation.Validator;
import @@NS@@.validation.Valid;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.context.annotation.Import;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import @@NS@@.validation.constraints.NotBlank;
import @@MAPPER@@;
import org.apache.ibatis.annotations.Select;
import org.apache.ibatis.session.SqlSession;
import org.apache.ibatis.session.SqlSessionFactory;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.context.ActiveProfiles;
import static org.junit.jupiter.api.Assertions.*;

/** Mechanical integration only: no business endpoint or production storage. */
@SpringBootTest(webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT)
@Import(PlatformIntegrationTest.ProbeController.class)
@ActiveProfiles("scaffold-test")
class PlatformIntegrationTest {
    @Value("${local.server.port}") int port;
    @Autowired Validator validator;
    public static class ProbePayload {
        @NotBlank private String value;
        public String getValue() { return value; }
        public void setValue(String value) { this.value = value; }
    }
    @RestController
    public static class ProbeController {
        @PostMapping(value = "/__scaffold_probe", consumes = "application/json", produces = "application/json")
        public ProbePayload echo(@Valid @RequestBody ProbePayload body) { return body; }
    }
    @Test void httpJsonRoundTrip() throws Exception { httpProbe("probe", 200); }
    @Test void httpValidationRejectsBlank() throws Exception { httpProbe("", 400); }
    private void httpProbe(String value, int expectedStatus) throws Exception {
        java.net.HttpURLConnection connection = (java.net.HttpURLConnection) new java.net.URL("http://127.0.0.1:" + port + "/__scaffold_probe").openConnection();
        connection.setConnectTimeout(5000); connection.setReadTimeout(5000);
        connection.setRequestMethod("POST"); connection.setRequestProperty("Content-Type", "application/json"); connection.setRequestProperty("Accept", "application/json"); connection.setDoOutput(true);
        ProbePayload request = new ProbePayload(); request.setValue(value);
        try {
            try (java.io.OutputStream stream = connection.getOutputStream()) { stream.write(mapper.writeValueAsBytes(request)); }
            assertEquals(expectedStatus, connection.getResponseCode());
            if (expectedStatus == 400) {
                try (java.io.InputStream stream = connection.getErrorStream()) {
                    assertNotNull(stream);
                    java.util.Map<?, ?> error = mapper.readValue(stream, java.util.Map.class);
                    assertFalse(error.isEmpty()); // Exact YSS error envelope requires the component capability contract.
                }
            }
            if (expectedStatus == 200) {
                assertTrue(connection.getContentType().startsWith("application/json"));
                try (java.io.InputStream stream = connection.getInputStream()) { assertEquals(value, mapper.readValue(stream, ProbePayload.class).getValue()); }
            }
        } finally { connection.disconnect(); }
    }
    @Autowired @@MAPPERSIMPLE@@ mapper;
    @Autowired SqlSessionFactory sqlSessionFactory;
    @Autowired org.springframework.jdbc.core.JdbcTemplate jdbc;
    @Autowired org.springframework.transaction.PlatformTransactionManager transactions;
    @Autowired org.mybatis.spring.SqlSessionTemplate sessions;
    @Autowired PlatformProbeMapping probeMapping;

    @lombok.Getter
    @lombok.Setter
    public static class ProbeRow {
        private Long id;
        private String value;
    }
    interface RoundTripMapper {
        @org.apache.ibatis.annotations.Insert("INSERT INTO scaffold_roundtrip(id, probe_value) VALUES(#{id}, #{value})")
        int insert(ProbeRow row);
        @org.apache.ibatis.annotations.Results(@org.apache.ibatis.annotations.Result(column = "probe_value", property = "value"))
        @Select("SELECT id, probe_value FROM scaffold_roundtrip ORDER BY id")
        java.util.List<ProbeRow> page(org.apache.ibatis.session.RowBounds bounds);
        @Select("SELECT COUNT(*) FROM scaffold_roundtrip") int count();
    }
    private RoundTripMapper roundTripMapper() {
        jdbc.execute("CREATE TABLE IF NOT EXISTS scaffold_roundtrip(id BIGINT PRIMARY KEY, probe_value VARCHAR(100) NOT NULL)");
        jdbc.update("DELETE FROM scaffold_roundtrip");
        if (!sqlSessionFactory.getConfiguration().hasMapper(RoundTripMapper.class)) sqlSessionFactory.getConfiguration().addMapper(RoundTripMapper.class);
        return sessions.getMapper(RoundTripMapper.class);
    }
    @Test void mappingPersistenceAndPaginationRoundTrip() {
        RoundTripMapper persistence = roundTripMapper();
        for (long id = 1; id <= 3; id++) {
            ProbeRow original = new ProbeRow(); original.setId(id); original.setValue("probe-" + id);
            ProbeRow copy = probeMapping.copy(original);
            assertNotSame(original, copy); assertEquals(original.getId(), copy.getId());
            persistence.insert(copy);
        }
        assertEquals(3, persistence.count());
        java.util.List<ProbeRow> page = persistence.page(new org.apache.ibatis.session.RowBounds(1, 1));
        assertEquals(1, page.size()); assertEquals(Long.valueOf(2), page.get(0).getId());
        assertEquals("probe-2", page.get(0).getValue());
        assertNull(probeMapping.copy(null));
    }
    @Test void transactionRollsBackOnUseCaseFailure() {
        final RoundTripMapper persistence = roundTripMapper();
        org.springframework.transaction.support.TransactionTemplate transaction = new org.springframework.transaction.support.TransactionTemplate(transactions);
        assertThrows(IllegalStateException.class, () -> transaction.execute(status -> {
            ProbeRow row = new ProbeRow(); row.setId(9L); row.setValue("must-rollback"); persistence.insert(row);
            throw new IllegalStateException("synthetic use-case failure");
        }));
        assertEquals(0, persistence.count());
    }
    static final class ValidationProbe { @NotBlank String value = ""; }
    @Test void validationAndJsonAutoConfiguration() throws Exception {
        assertFalse(validator.validate(new ValidationProbe()).isEmpty());
        assertEquals("\"probe\"", mapper.writeValueAsString("probe"));
    }
    interface ProbeMapper { @Select("SELECT 1") int probe(); }
    @Test void mybatisCanMapVerificationQuery() throws Exception {
        try (SqlSession session = sqlSessionFactory.openSession()) {
            if (!session.getConfiguration().hasMapper(ProbeMapper.class)) session.getConfiguration().addMapper(ProbeMapper.class);
            assertEquals(1, session.getMapper(ProbeMapper.class).probe());
        }
    }
@@FEIGN@@}

@org.mapstruct.Mapper(componentModel = "spring", unmappedTargetPolicy = org.mapstruct.ReportingPolicy.ERROR)
interface PlatformProbeMapping {
    @org.mapstruct.Mapping(target = "id", source = "id")
    @org.mapstruct.Mapping(target = "value", source = "value")
    PlatformIntegrationTest.ProbeRow copy(PlatformIntegrationTest.ProbeRow row);
}
`

const smokeFeignBlock = `    @Autowired org.springframework.cloud.openfeign.@@FEIGNCLASS@@ feignFactory;
    @Test void feignCanDecodeJson() throws Exception {
        feign.codec.Decoder decoder = feignFactory.getInstance("platform-probe", feign.codec.Decoder.class);
        assertNotNull(decoder);
        feign.Request request = feign.Request.create(feign.Request.HttpMethod.GET, "http://localhost/probe", java.util.Collections.emptyMap(), (byte[]) null, java.nio.charset.StandardCharsets.UTF_8, null);
        feign.Response response = feign.Response.builder().request(request).status(200).reason("OK")
            .headers(java.util.Collections.singletonMap("Content-Type", java.util.Collections.singletonList("application/json")))
            .body("{}", java.nio.charset.StandardCharsets.UTF_8).build();
        assertTrue(decoder.decode(response, java.util.Map.class) instanceof java.util.Map);
    }
`
