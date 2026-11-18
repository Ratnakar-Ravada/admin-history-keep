package com.locationkeeper.app

import android.app.Activity
import android.app.Application
import android.graphics.Bitmap
import android.graphics.Canvas
import android.os.Looper
import android.view.View
import android.widget.Button
import android.view.ViewGroup
import androidx.test.core.app.ApplicationProvider
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.io.File
import java.time.Duration

/** Disposable visual-review artifacts in the OS temp directory; no GPS, backend, or app data. */
@RunWith(RobolectricTestRunner::class)
@Config(application=Application::class,sdk=[28],manifest=Config.NONE,qualifiers="w393dp-h852dp-xxhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class UiPreviewTest {
    private fun all(view: View): List<View> = listOf(view) +
        if(view is ViewGroup) (0 until view.childCount).flatMap { all(view.getChildAt(it)) } else emptyList()
    private fun render(activity: Activity,name: String) {
        shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(400))
        val view=activity.window.decorView
        view.measure(View.MeasureSpec.makeMeasureSpec(1179,View.MeasureSpec.EXACTLY),
            View.MeasureSpec.makeMeasureSpec(2556,View.MeasureSpec.EXACTLY))
        view.layout(0,0,1179,2556)
        shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(400))
        val bitmap=Bitmap.createBitmap(1179,2556,Bitmap.Config.ARGB_8888)
        view.draw(Canvas(bitmap))
        File("/private/tmp/locationkeeper-$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG,100,it) }
        bitmap.recycle()
    }
    @Test fun reviewSetupAndRecordsLayout() {
        val context=ApplicationProvider.getApplicationContext<Application>()
        context.getSharedPreferences("device_profile",0).edit().clear().commit()
        val setup=Robolectric.buildActivity(OnboardingActivity::class.java).create().start().visible()
        val activity=setup.get()
        render(activity,"permission")
        fun next() { all(activity.findViewById(android.R.id.content)).filterIsInstance<Button>()
            .single { it.text=="Set this permission later" }.performClick() }
        next(); render(activity,"background")
        next(); render(activity,"name")
        setup.stop().destroy()
        DeviceProfile(context).save("Everyday phone")
        val main=Robolectric.buildActivity(MainActivity::class.java).create().start().visible()
        render(main.get(),"records")
        main.stop().destroy()
        context.getSharedPreferences("device_profile",0).edit().clear().commit()
    }
}
