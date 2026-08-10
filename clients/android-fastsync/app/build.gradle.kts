plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

val appVersion = "0.1"

// Names the APK after the product instead of the module. Both Android apps live in a module
// called "app", so both built an "app-debug.apk" — and the release workflow copies every APK
// it finds into one directory, where the second one silently replaced the first.
base { archivesName.set("discodrive-fastsync-$appVersion") }

android {
    namespace = "org.discodrive.fastsync"
    compileSdk = 34
    defaultConfig {
        applicationId = "org.discodrive.fastsync"
        minSdk = 30
        targetSdk = 34
        versionCode = 1
        versionName = appVersion
    }
    buildFeatures { compose = true }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
}
dependencies {
    implementation(files("libs/kfmobile.aar"))
    implementation(platform("androidx.compose:compose-bom:2024.09.03"))
    implementation("androidx.activity:activity-compose:1.9.2")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.8.6")
    implementation("androidx.work:work-runtime-ktx:2.9.1")
    implementation("androidx.security:security-crypto:1.1.0-alpha06")
    implementation("androidx.core:core-ktx:1.13.1")
}
