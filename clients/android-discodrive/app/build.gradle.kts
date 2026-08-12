plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

val appVersion = "0.0.5"

// Names the APK after the product instead of the module. Both Android apps live in a module
// called "app", so both built an "app-debug.apk" — and the release workflow copies every APK
// it finds into one directory, where the second one silently replaced the first.
base { archivesName.set("discodrive-$appVersion") }

android {
    namespace = "org.discodrive.android"
    compileSdk = 34
    defaultConfig {
        applicationId = "org.discodrive.android"
        minSdk = 30
        targetSdk = 34
        // Bumped on every release: Android refuses to install a build over one with the
        // same code, so leaving it behind means updating only by reinstalling.
        versionCode = 2
        versionName = appVersion
    }
    buildFeatures { compose = true; buildConfig = true }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
}
dependencies {
    implementation(files("libs/kfmobile.aar"))
    implementation("androidx.appcompat:appcompat:1.7.0")
    implementation(platform("androidx.compose:compose-bom:2024.09.03"))
    implementation("androidx.activity:activity-compose:1.9.2")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.8.6")
    implementation("androidx.security:security-crypto:1.1.0-alpha06")
    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.work:work-runtime-ktx:2.9.1")
    testImplementation("junit:junit:4.13.2")
    // android.jar's org.json is a stub that throws in unit tests; the real implementation
    // lets the rule-serialization tests run on the JVM.
    testImplementation("org.json:json:20240303")
}
