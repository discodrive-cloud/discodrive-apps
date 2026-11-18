plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

// Release CI stamps both from the tag (DISCODRIVE_VERSION_NAME / DISCODRIVE_VERSION_CODE).
// A local build derives them from the latest v* tag the same way, so it no longer shows a
// version frozen in this file: on the tag itself "0.1.7", past it "0.1.7-3-gabc1234", with
// uncommitted changes "-dirty" on top. versionCode = major*1000000 + minor*1000 + patch, as
// in release.yml.
val gitVersion: String? = runCatching {
    providers.exec {
        commandLine("git", "describe", "--tags", "--match", "v[0-9]*", "--dirty")
        isIgnoreExitValue = true
    }.standardOutput.asText.get().trim().removePrefix("v").ifEmpty { null }
}.getOrNull()
val appVersion: String = System.getenv("DISCODRIVE_VERSION_NAME")?.takeIf { it.isNotBlank() }
    ?: gitVersion ?: "0.0.0-dev"
val appVersionCode: Int = System.getenv("DISCODRIVE_VERSION_CODE")?.takeIf { it.isNotBlank() }?.let {
    requireNotNull(it.toIntOrNull()?.takeIf { code -> code > 0 }) {
        "DISCODRIVE_VERSION_CODE must be a positive integer, got '$it'"
    }
} ?: Regex("""^(\d+)\.(\d+)\.(\d+)""").find(appVersion)?.destructured
    ?.let { (major, minor, patch) -> major.toInt() * 1_000_000 + minor.toInt() * 1_000 + patch.toInt() }
    ?.takeIf { it > 0 } ?: 1

// Names the APK after the product instead of the module. Both Android apps live in a module
// called "app", so both built an "app-debug.apk" — and the release workflow copies every APK
// it finds into one directory, where the second one silently replaced the first.
base { archivesName.set("discodrive-$appVersion") }

android {
    namespace = "org.discodrive.android"
    compileSdk = 36
    defaultConfig {
        applicationId = "org.discodrive.android"
        minSdk = 30
        targetSdk = 36
        // Bumped on every release: Android refuses to install a build over one with the
        // same code, so leaving it behind means updating only by reinstalling.
        versionCode = appVersionCode
        versionName = appVersion
    }
    // Releases are signed with one long-lived key so updates install over each other.
    // The keystore stays out of the repo: CI decodes it from a secret and points these
    // variables at it. Without them a release build is left unsigned and not shipped.
    val keystore = System.getenv("DISCODRIVE_KEYSTORE")
    signingConfigs {
        if (keystore != null) {
            create("release") {
                storeFile = file(keystore)
                storePassword = System.getenv("DISCODRIVE_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("DISCODRIVE_KEY_ALIAS")
                keyPassword = System.getenv("DISCODRIVE_KEY_PASSWORD")
            }
        }
    }
    buildTypes {
        release {
            // Not debuggable (the release default). R8 shrinks and obfuscates: Play flags a
            // bundle without it. The gomobile bindings are reached through JNI by name; the
            // aar keeps go.** and mobile.** itself, proguard-rules.pro covers our side. The
            // mapping file goes into the bundle, so Play deobfuscates crash reports.
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            if (keystore != null) signingConfig = signingConfigs.getByName("release")
        }
    }
    // Two builds from one code base, the same package and the same key. "full" is the APK on
    // GitHub: All Files Access, so the sync folder and auto-upload read ordinary paths.
    // "play" is the Google Play bundle: Play only grants that permission to a few kinds of
    // app, so this build does not ask for it and leaves out what depends on it.
    flavorDimensions += "store"
    productFlavors {
        create("full") {
            dimension = "store"
            buildConfigField("boolean", "ALL_FILES_ACCESS", "true")
        }
        create("play") {
            dimension = "store"
            buildConfigField("boolean", "ALL_FILES_ACCESS", "false")
        }
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
    implementation("androidx.activity:activity-compose:1.10.1")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.8.6")
    implementation("androidx.security:security-crypto:1.1.0-alpha06")
    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.work:work-runtime-ktx:2.10.5")
    // In-app review, Google Play build only (src/play/StoreReview.kt): the GitHub APK does not
    // depend on Play services.
    "playImplementation"("com.google.android.play:review:2.0.2")
    testImplementation("junit:junit:4.13.2")
    // android.jar's org.json is a stub that throws in unit tests; the real implementation
    // lets the rule-serialization tests run on the JVM.
    testImplementation("org.json:json:20240303")
}
