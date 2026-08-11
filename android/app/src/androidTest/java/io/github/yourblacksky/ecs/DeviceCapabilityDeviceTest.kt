package io.github.yourblacksky.ecs

import android.os.Build
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeFalse
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Hardware-only coverage that does not launch MainActivity. This keeps device
 * capability verification usable on Android variants that block background
 * activity launches from instrumentation.
 */
@RunWith(AndroidJUnit4::class)
class DeviceCapabilityDeviceTest {
    @Test
    fun allAvailableDeviceCapabilitiesExecute() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val capabilities = AndroidDeviceCapabilityProvider(context)
        try {
            if (capabilities.available("flashlight")) {
                assertEquals(
                    "on",
                    JSONObject(
                        capabilities.execute("flashlight", """{"action":"on"}"""),
                    ).getString("state"),
                )
                assertEquals(
                    "off",
                    JSONObject(
                        capabilities.execute("flashlight", """{"action":"off"}"""),
                    ).getString("state"),
                )
            }

            assertTrue(
                JSONObject(
                    capabilities.execute("vibrate", """{"pattern":"double"}"""),
                ).getBoolean("performed"),
            )
            assertEquals(
                2,
                JSONObject(capabilities.execute("beep", """{"count":2}""")).getInt("count"),
            )

            val brightness = JSONObject(
                capabilities.execute("screen_brightness", """{"action":"get"}"""),
            )
            assertTrue(brightness.getDouble("value") in 0.0..1.0)

            assertTrue(
                JSONObject(
                    capabilities.execute(
                        "notify",
                        """{"title":"ECS 真机测试","body":"设备能力调用成功"}""",
                    ),
                ).getBoolean("delivered"),
            )

        } finally {
            capabilities.close()
        }
    }

    @Test
    fun locationExecutesOnPhysicalDevice() {
        assumeFalse("Android emulator has no deterministic location fixture", isEmulator())
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val capabilities = AndroidDeviceCapabilityProvider(context)
        try {
            assumeFalse("Location capability is unavailable", !capabilities.available("location"))
            val location = JSONObject(
                capabilities.execute("location", """{"accuracy":"coarse"}"""),
            )
            assertTrue(location.getDouble("latitude") in -90.0..90.0)
            assertTrue(location.getDouble("longitude") in -180.0..180.0)
            assertTrue(location.getDouble("accuracy_m") >= 0)
        } finally {
            capabilities.close()
        }
    }

    private fun isEmulator(): Boolean =
        Build.FINGERPRINT.startsWith("generic") ||
            Build.FINGERPRINT.contains("emulator") ||
            Build.MODEL.contains("Emulator") ||
            Build.MODEL.contains("Android SDK built for") ||
            Build.HARDWARE.contains("goldfish") ||
            Build.HARDWARE.contains("ranchu") ||
            Build.PRODUCT.startsWith("sdk_") ||
            Build.DEVICE.startsWith("emu")
}
