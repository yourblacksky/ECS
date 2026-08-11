package io.github.yourblacksky.ecs

import org.junit.Assert.assertEquals
import org.junit.Test

class EcsSettingsMigrationTest {
    @Test
    fun officialDeepSeekLegacyModelIsMigrated() {
        assertEquals(
            "deepseek-v4-flash",
            EcsSettings.migrateLegacyDeepSeekModel(
                "https://api.deepseek.com/",
                "deepseek-chat",
            ),
        )
    }

    @Test
    fun customProviderAndModelArePreserved() {
        assertEquals(
            "deepseek-chat",
            EcsSettings.migrateLegacyDeepSeekModel(
                "https://provider.example/v1",
                "deepseek-chat",
            ),
        )
        assertEquals(
            "custom-model",
            EcsSettings.migrateLegacyDeepSeekModel(
                "https://api.deepseek.com",
                "custom-model",
            ),
        )
    }
}
