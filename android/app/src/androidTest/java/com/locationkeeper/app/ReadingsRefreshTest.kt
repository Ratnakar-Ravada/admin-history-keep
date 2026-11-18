package com.locationkeeper.app

import android.os.SystemClock
import android.widget.Button
import android.widget.CheckBox
import android.widget.TextView
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith

/** Read-only against real location history; checks refresh completion, not just a ticking label. */
@RunWith(AndroidJUnit4::class)
class ReadingsRefreshTest {
    @Test fun automaticAndManualRefreshCompleteWithoutCreatingRecords() {
        val scenario = ActivityScenario.launch(MainActivity::class.java)
        var originalAuto = true
        try {
            scenario.onActivity {
                val box = it.findViewById<CheckBox>(R.id.auto_refresh_readings)
                originalAuto = box.isChecked
                box.isChecked = true
            }
            fun timestamp(): String {
                var value = ""
                scenario.onActivity {
                    value = it.findViewById<TextView>(R.id.readings_refresh_status).text.toString().substringBefore(" ·")
                }
                return value
            }
            fun await(timeoutMs: Long, condition: () -> Boolean) {
                val deadline = SystemClock.elapsedRealtime() + timeoutMs
                while (!condition()) {
                    if (SystemClock.elapsedRealtime() >= deadline) fail("Refresh did not complete")
                    SystemClock.sleep(100)
                }
            }
            await(5_000) { timestamp().startsWith("Last updated") }
            val first = timestamp()
            await(38_000) { timestamp().startsWith("Last updated") && timestamp() != first }
            scenario.onActivity { it.findViewById<CheckBox>(R.id.auto_refresh_readings).isChecked = false }
            val automatic = timestamp()
            // Ensure a new displayed second, then request a refresh while auto refresh is paused.
            SystemClock.sleep(1_100)
            scenario.onActivity {
                assertTrue(it.findViewById<Button>(R.id.refresh_readings).isEnabled)
                it.findViewById<Button>(R.id.refresh_readings).performClick()
            }
            await(5_000) { timestamp().startsWith("Last updated") && timestamp() != automatic }
            scenario.onActivity {
                assertTrue(it.findViewById<TextView>(R.id.readings_refresh_status).text.toString().contains("paused"))
            }
        } finally {
            scenario.onActivity { it.findViewById<CheckBox>(R.id.auto_refresh_readings).isChecked = originalAuto }
            scenario.close()
        }
    }
}
