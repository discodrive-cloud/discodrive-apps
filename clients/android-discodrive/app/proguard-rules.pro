# R8 rules for the release build.
#
# kfmobile.aar (gomobile) ships its own: -keep class go.** and mobile.**. What it cannot
# cover is our code that Go calls back into: Go looks Java methods up by name through JNI,
# so a Kotlin implementation of a mobile.* interface must keep its class and method names.
-keep class * implements go.Seq$Proxy { *; }
-keep class * implements mobile.** { *; }

# Readable stack traces in Play's crash reports (deobfuscated with the bundled mapping).
-keepattributes SourceFile,LineNumberTable
-renamesourcefileattribute SourceFile

# Tink (under androidx.security:security-crypto) refers to compile-time-only annotations
# that are not shipped at runtime; nothing calls them.
-dontwarn com.google.errorprone.annotations.**
-dontwarn javax.annotation.**
