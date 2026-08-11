package io.github.yourblacksky.ecs

import android.Manifest
import android.os.Build
import android.view.View
import android.widget.CompoundButton
import android.widget.EditText
import android.widget.TextView
import androidx.test.espresso.Espresso.onView
import androidx.test.espresso.ViewAction
import androidx.test.espresso.ViewAssertion
import androidx.test.espresso.action.ViewActions.click
import androidx.test.espresso.action.ViewActions.closeSoftKeyboard
import androidx.test.espresso.action.ViewActions.replaceText
import androidx.test.espresso.matcher.ViewMatchers.isDisplayed
import androidx.test.espresso.matcher.ViewMatchers.withId
import androidx.test.espresso.matcher.ViewMatchers.withText
import androidx.test.ext.junit.rules.ActivityScenarioRule
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.filters.LargeTest
import androidx.test.rule.GrantPermissionRule
import io.github.yourblacksky.ecs.mobile.Mobile
import org.hamcrest.Matcher
import org.hamcrest.Matchers.containsString
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.RuleChain
import java.net.HttpURLConnection
import java.net.URL

@LargeTest
class MainActivityTest {
    private val permissions = if (Build.VERSION.SDK_INT >= 33) {
        GrantPermissionRule.grant(
            Manifest.permission.CAMERA,
            Manifest.permission.ACCESS_COARSE_LOCATION,
            Manifest.permission.ACCESS_FINE_LOCATION,
            Manifest.permission.POST_NOTIFICATIONS,
            Manifest.permission.NEARBY_WIFI_DEVICES,
        )
    } else {
        GrantPermissionRule.grant(
            Manifest.permission.CAMERA,
            Manifest.permission.ACCESS_COARSE_LOCATION,
            Manifest.permission.ACCESS_FINE_LOCATION,
        )
    }
    private val activity = ActivityScenarioRule(MainActivity::class.java)

    @get:Rule
    val rules: RuleChain = RuleChain.outerRule(permissions).around(activity)

    @Test
    fun runtimeCanStartStopAndReportHealth() {
        ensureStopped()

        onView(withId(R.id.ecs_start)).perform(click())
        waitForText(R.id.ecs_status, containsString("运行中"))
        val runtimeStatus = JSONObject(Mobile.statusJSON())
        val tools = runtimeStatus.getInt("tools")
        val skills = runtimeStatus.getInt("skills")
        assertTrue("runtime should expose tools", tools >= 10)
        assertTrue("runtime should expose skills", skills >= 10)
        waitForText(R.id.ecs_status, containsString("$tools Tool / $skills Skill"))
        assertEquals(200, healthStatus())

        onView(withId(R.id.ecs_stop)).perform(click())
        waitForText(R.id.ecs_status, containsString("已停止"))
    }

    @Test
    fun openingAdminKeepsRuntimeAlive() {
        ensureRunning()
        onView(withId(R.id.ecs_open_admin)).perform(click())

        waitForView(withId(R.id.ecs_admin_webview), isDisplayed())
        waitForText(R.id.ecs_status, containsString("运行中"))
        assertEquals(200, healthStatus())
    }

    @Test
    fun copyingCredentialShowsConfirmation() {
        onView(withId(R.id.ecs_node_settings)).perform(click())
        onView(withId(R.id.settings_copy_admin_token)).perform(click())
        waitForText(R.id.settings_copy_admin_token, containsString("已复制"))
    }

    @Test
    fun modelCredentialFieldIsMasked() {
        onView(withId(R.id.ecs_node_settings)).perform(click())
        onView(withId(R.id.settings_api_key)).check { view, error ->
            if (error != null) throw error
            val input = view as EditText
            assertTrue("API key must use a password transformation", input.transformationMethod != null)
            assertEquals(
                View.IMPORTANT_FOR_AUTOFILL_NO_EXCLUDE_DESCENDANTS,
                input.importantForAutofill,
            )
        }
    }

    @Test
    fun settingsCanBeEditedSavedAndRestored() {
        onView(withId(R.id.ecs_node_settings)).perform(click())
        val originalBaseUrl = textOf(R.id.settings_base_url)
        val originalModel = textOf(R.id.settings_model)
        val originalDistributed = checkedOf(R.id.settings_distributed)
        val originalAutoStart = checkedOf(R.id.settings_auto_start)

        onView(withId(R.id.settings_base_url))
            .perform(replaceText("https://example.invalid/v1"), closeSoftKeyboard())
        onView(withId(R.id.settings_model))
            .perform(replaceText("ecs-ui-test-model"), closeSoftKeyboard())
        onView(withId(R.id.settings_distributed)).perform(click())
        onView(withId(R.id.settings_auto_start)).perform(click())
        onView(withText("保存")).perform(click())

        onView(withId(R.id.ecs_node_settings)).perform(click())
        onView(withId(R.id.settings_base_url))
            .check(matchesText("https://example.invalid/v1"))
        onView(withId(R.id.settings_model)).check(matchesText("ecs-ui-test-model"))

        onView(withId(R.id.settings_base_url))
            .perform(replaceText(originalBaseUrl), closeSoftKeyboard())
        onView(withId(R.id.settings_model))
            .perform(replaceText(originalModel), closeSoftKeyboard())
        if (checkedOf(R.id.settings_distributed) != originalDistributed) {
            onView(withId(R.id.settings_distributed)).perform(click())
        }
        if (checkedOf(R.id.settings_auto_start) != originalAutoStart) {
            onView(withId(R.id.settings_auto_start)).perform(click())
        }
        onView(withText("保存")).perform(click())
    }

    @Test
    fun nativeDeviceProviderCanTurnFlashlightOnAndOff() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val capabilities = AndroidDeviceCapabilityProvider(context)
        if (!capabilities.available("flashlight")) {
            capabilities.close()
            return
        }
        try {
            val enabled = JSONObject(
                capabilities.execute("flashlight", """{"action":"on"}"""),
            )
            assertTrue(enabled.getBoolean("available"))
            assertEquals("on", enabled.getString("state"))

            val disabled = JSONObject(
                capabilities.execute("flashlight", """{"action":"off"}"""),
            )
            assertFalse(disabled.getString("state") == "on")
        } finally {
            capabilities.close()
        }
    }

    @Test
    fun nativeDeviceProviderRunsNonLocationCapabilities() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val capabilities = AndroidDeviceCapabilityProvider(context)
        try {
            val brightness = JSONObject(
                capabilities.execute("screen_brightness", """{"action":"get"}"""),
            )
            assertTrue(brightness.getDouble("value") in 0.0..1.0)

            val vibration = JSONObject(
                capabilities.execute("vibrate", """{"pattern":"short"}"""),
            )
            assertTrue(vibration.getBoolean("performed"))

            val beep = JSONObject(capabilities.execute("beep", """{"count":1}"""))
            assertEquals(1, beep.getInt("count"))

            val notification = JSONObject(
                capabilities.execute(
                    "notify",
                    """{"title":"ECS Test","body":"Device capability test"}""",
                ),
            )
            assertTrue(notification.getBoolean("delivered"))
        } finally {
            capabilities.close()
        }
    }

    private fun ensureRunning() {
        if (!textOf(R.id.ecs_status).contains("运行中")) {
            onView(withId(R.id.ecs_start)).perform(click())
        }
        waitForText(R.id.ecs_status, containsString("运行中"))
    }

    private fun ensureStopped() {
        onView(withId(R.id.ecs_stop)).perform(click())
        waitForText(R.id.ecs_status, containsString("已停止"))
    }

    private fun healthStatus(): Int {
        val connection = URL("http://127.0.0.1:18090/healthz")
            .openConnection() as HttpURLConnection
        connection.connectTimeout = 5_000
        connection.readTimeout = 5_000
        return connection.use { it.responseCode }
    }

    private fun textOf(id: Int): String {
        var value = ""
        onView(withId(id)).perform(object : ViewAction {
            override fun getConstraints(): Matcher<View> = isDisplayed()
            override fun getDescription() = "read text"
            override fun perform(
                controller: androidx.test.espresso.UiController,
                view: View,
            ) {
                value = (view as TextView).text.toString()
            }
        })
        return value
    }

    private fun checkedOf(id: Int): Boolean {
        var value = false
        onView(withId(id)).perform(object : ViewAction {
            override fun getConstraints(): Matcher<View> = isDisplayed()
            override fun getDescription() = "read checked state"
            override fun perform(
                controller: androidx.test.espresso.UiController,
                view: View,
            ) {
                value = (view as CompoundButton).isChecked
            }
        })
        return value
    }

    private fun matchesText(expected: String) = ViewAssertion { view, _ ->
        assertEquals(expected, (view as TextView).text.toString())
    }

    private fun waitForText(id: Int, matcher: Matcher<String>) {
        waitForView(withId(id), withText(matcher))
    }

    private fun waitForView(viewMatcher: Matcher<View>, assertionMatcher: Matcher<View>) {
        val deadline = System.currentTimeMillis() + 10_000
        var lastFailure: Throwable? = null
        while (System.currentTimeMillis() < deadline) {
            try {
                onView(viewMatcher).check { view, error ->
                    if (error != null) throw error
                    if (!assertionMatcher.matches(view)) {
                        throw AssertionError("View did not match $assertionMatcher")
                    }
                }
                return
            } catch (failure: Throwable) {
                lastFailure = failure
                Thread.sleep(200)
            }
        }
        throw AssertionError("Timed out waiting for $assertionMatcher", lastFailure)
    }
}

private inline fun <T : HttpURLConnection, R> T.use(block: (T) -> R): R =
    try {
        block(this)
    } finally {
        disconnect()
    }
