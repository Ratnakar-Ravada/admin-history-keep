package com.locationkeeper.app

import android.animation.ValueAnimator
import android.app.Activity
import android.content.Context
import android.content.res.ColorStateList
import android.graphics.*
import android.graphics.drawable.GradientDrawable
import android.graphics.drawable.RippleDrawable
import android.os.Build
import android.view.Gravity
import android.view.View
import android.view.WindowInsets
import android.widget.*

/** Static View styling: no animation loops, background work or network requests. */
object KeeperUi {
    val background=Color.rgb(245,247,244)
    val ink=Color.rgb(23,48,39)
    val muted=Color.rgb(105,123,114)
    val green=Color.rgb(28,107,77)
    val soft=Color.rgb(232,241,233)
    val line=Color.rgb(222,231,223)
    val red=Color.rgb(183,65,57)
    fun dp(context: Context,value: Int)=(value*context.resources.displayMetrics.density).toInt()
    fun shape(context: Context,color: Int,radius: Int=24,border: Int?=null)=GradientDrawable().apply {
        setColor(color); cornerRadius=dp(context,radius).toFloat()
        border?.let { setStroke(dp(context,1),it) }
    }
    @Suppress("DEPRECATION")
    fun page(activity: Activity): LinearLayout {
        val scroll=ScrollView(activity).apply {
            setBackgroundColor(KeeperUi.background); isFillViewport=true; clipToPadding=false
            setPadding(dp(activity,24),dp(activity,12),dp(activity,24),0)
            overScrollMode=View.OVER_SCROLL_IF_CONTENT_SCROLLS
        }
        val frame=FrameLayout(activity)
        val content=LinearLayout(activity).apply {
            orientation=LinearLayout.VERTICAL
            setPadding(0,dp(activity,12),0,dp(activity,32))
        }
        frame.addView(content,FrameLayout.LayoutParams(-1,-2,Gravity.TOP or Gravity.CENTER_HORIZONTAL))
        scroll.addView(frame)
        frame.addOnLayoutChangeListener { view,l,_,right,_,oldL,_,oldRight,_ ->
            if (right-l != oldRight-oldL) {
                val width=minOf(view.width,dp(activity,640))
                if (width>0 && content.layoutParams.width!=width) {
                    content.layoutParams=(content.layoutParams as FrameLayout.LayoutParams).apply { this.width=width }
                }
            }
        }
        if (Build.VERSION.SDK_INT>=30) activity.window.setDecorFitsSystemWindows(false)
        activity.window.decorView.systemUiVisibility=View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR or View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR
        scroll.setOnApplyWindowInsetsListener { view,insets ->
            if (Build.VERSION.SDK_INT>=30) {
                val bars=insets.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout() or WindowInsets.Type.ime())
                view.setPadding(dp(activity,24)+bars.left,bars.top+dp(activity,12),dp(activity,24)+bars.right,bars.bottom)
            } else {
                view.setPadding(dp(activity,24)+insets.systemWindowInsetLeft,insets.systemWindowInsetTop+dp(activity,12),
                    dp(activity,24)+insets.systemWindowInsetRight,insets.systemWindowInsetBottom)
            }
            insets
        }
        activity.setContentView(scroll)
        return content
    }
    fun text(context: Context,value: String,size: Float=15f,color: Int=muted,bold: Boolean=false)=TextView(context).apply {
        text=value; textSize=size; setTextColor(color); setLineSpacing(dp(context,3).toFloat(),1f)
        if (bold) typeface=Typeface.create("sans-serif-medium",Typeface.NORMAL)
    }
    fun card(context: Context,color: Int=Color.WHITE)=LinearLayout(context).apply {
        orientation=LinearLayout.VERTICAL; setPadding(dp(context,22),dp(context,22),dp(context,22),dp(context,22))
        background=shape(context,color,24,if (color==Color.WHITE) line else null)
    }
    fun button(context: Context,title: String,primary: Boolean=true,action: ()->Unit)=Button(context).apply {
        text=title; isAllCaps=false; textSize=15f; typeface=Typeface.create("sans-serif-medium",Typeface.NORMAL)
        minHeight=dp(context,54); minimumHeight=dp(context,54)
        setPadding(dp(context,18),dp(context,12),dp(context,18),dp(context,12))
        setTextColor(if (primary) Color.WHITE else green)
        background=RippleDrawable(ColorStateList.valueOf(0x22000000),shape(context,if (primary) green else soft,16),null)
        setOnClickListener { action() }
    }
    fun link(context: Context,title: String,action: ()->Unit)=button(context,title,false,action).apply {
        background=RippleDrawable(ColorStateList.valueOf(0x18000000),shape(context,Color.TRANSPARENT,16),null)
    }
    fun enter(view: View) {
        if (!ValueAnimator.areAnimatorsEnabled()) return
        view.alpha=0f; view.translationY=dp(view.context,10).toFloat()
        view.animate().alpha(1f).translationY(0f).setDuration(260).start()
    }
}

/** A small, static location mark. Drawing is invalidated only by normal View layout. */
class KeeperMark(context: Context): View(context) {
    private val paint=Paint(Paint.ANTI_ALIAS_FLAG)
    override fun onDraw(canvas: Canvas) {
        super.onDraw(canvas)
        val cx=width/2f; val cy=height/2f; val radius=minOf(width,height)*0.43f
        paint.style=Paint.Style.FILL; paint.color=KeeperUi.soft; canvas.drawCircle(cx,cy,radius,paint)
        paint.style=Paint.Style.STROKE; paint.strokeWidth=radius*0.06f; paint.color=KeeperUi.green
        canvas.drawCircle(cx,cy,radius*0.47f,paint)
        canvas.drawLine(cx,cy-radius*0.68f,cx,cy-radius*0.34f,paint)
        canvas.drawLine(cx,cy+radius*0.34f,cx,cy+radius*0.68f,paint)
        canvas.drawLine(cx-radius*0.68f,cy,cx-radius*0.34f,cy,paint)
        canvas.drawLine(cx+radius*0.34f,cy,cx+radius*0.68f,cy,paint)
        paint.style=Paint.Style.FILL; canvas.drawCircle(cx,cy,radius*0.13f,paint)
    }
}

fun <T: View> T.top(value: Int): T=apply {
    layoutParams=LinearLayout.LayoutParams(-1,-2).apply { topMargin=KeeperUi.dp(context,value) }
}
