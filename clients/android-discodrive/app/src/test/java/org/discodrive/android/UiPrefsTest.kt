package org.discodrive.android

import androidx.appcompat.app.AppCompatDelegate
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class UiPrefsTest {
    @Test
    fun `theme follows the system until chosen`() {
        assertEquals(ThemeMode.SYSTEM, UiPrefs(FakePrefs()).themeMode)
    }

    @Test
    fun `chosen theme is kept`() {
        val sp = FakePrefs()
        UiPrefs(sp).themeMode = ThemeMode.DARK
        assertEquals(ThemeMode.DARK, UiPrefs(sp).themeMode)
    }

    @Test
    fun `an unknown stored theme falls back to the system`() {
        val sp = FakePrefs()
        sp.edit().putString("themeMode", "SEPIA").commit()
        assertEquals(ThemeMode.SYSTEM, UiPrefs(sp).themeMode)
    }

    @Test
    fun `theme modes map to AppCompat night modes`() {
        assertEquals(AppCompatDelegate.MODE_NIGHT_FOLLOW_SYSTEM, ThemeMode.SYSTEM.nightMode)
        assertEquals(AppCompatDelegate.MODE_NIGHT_NO, ThemeMode.LIGHT.nightMode)
        assertEquals(AppCompatDelegate.MODE_NIGHT_YES, ThemeMode.DARK.nightMode)
    }

    @Test
    fun `active days count each day once`() {
        val ui = UiPrefs(FakePrefs())
        ui.recordActiveDay(100)
        ui.recordActiveDay(100)
        ui.recordActiveDay(101)
        assertEquals(ReviewState(firstDay = 100, lastDay = 101, activeDays = 2, askedDay = null), ui.reviewState)
    }

    @Test
    fun `asking is remembered`() {
        val ui = UiPrefs(FakePrefs())
        ui.recordActiveDay(100)
        ui.markReviewAsked(110)
        assertEquals(110L, ui.reviewState.askedDay)
    }
}

class ReviewPolicyTest {
    private fun state(first: Long, days: Int, asked: Long? = null) =
        ReviewState(firstDay = first, lastDay = first + days, activeDays = days, askedDay = asked)

    @Test
    fun `never on a fresh install`() {
        assertFalse(ReviewPolicy.shouldAsk(ReviewState(), today = 0))
        assertFalse(ReviewPolicy.shouldAsk(state(first = 100, days = 1), today = 100))
    }

    @Test
    fun `not before a week has passed even with many active days`() {
        assertFalse(ReviewPolicy.shouldAsk(state(first = 100, days = 6), today = 106))
    }

    @Test
    fun `not when used on too few days`() {
        assertFalse(ReviewPolicy.shouldAsk(state(first = 100, days = 3), today = 130))
    }

    @Test
    fun `asks after a week of regular use`() {
        assertTrue(ReviewPolicy.shouldAsk(state(first = 100, days = 4), today = 107))
    }

    @Test
    fun `asks again only after a long pause`() {
        assertFalse(ReviewPolicy.shouldAsk(state(first = 100, days = 10, asked = 110), today = 200))
        assertTrue(ReviewPolicy.shouldAsk(state(first = 100, days = 10, asked = 110), today = 230))
    }
}
