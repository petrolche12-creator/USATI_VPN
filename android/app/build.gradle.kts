plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "ru.usatyi.vpn"
    compileSdk = 34

    defaultConfig {
        applicationId = "ru.usatyi.vpn"
        minSdk = 28
        targetSdk = 34
        versionCode = 9
        versionName = "1.3.0"

        // Ядро цепочки Amnezia лежит в jniLibs как libusatyi_core.so: только arm64 (Go без cgo).
        ndk {
            abiFilters += "arm64-v8a"
        }
    }

    // Ключ подписи для личной сборки. Нужен, чтобы обновления ставились поверх старой версии.
    signingConfigs {
        create("release") {
            storeFile = file("usatyi-release.jks")
            storePassword = "usatyi-vpn-2026"
            keyAlias = "usatyi"
            keyPassword = "usatyi-vpn-2026"
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.getByName("release")
        }
    }

    buildFeatures {
        viewBinding = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
    }

    testOptions {
        unitTests.isReturnDefaultValues = true
    }
}

dependencies {
    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.appcompat:appcompat:1.7.0")
    implementation("com.google.android.material:material:1.12.0")
    implementation("com.jcraft:jsch:0.1.55")
    implementation("com.wireguard.android:tunnel:1.0.20230706")

    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20231013")
}

// Живая проверка против сервера: ./gradlew test -Pmtp.test.key=mtp://...
// Живая проверка SSH: ./gradlew testDebugUnitTest -Pmtp.test.ssh="host|port|user|password"
tasks.withType<org.gradle.api.tasks.testing.Test>().configureEach {
    // тесты лёгкие; маленькая куча нужна, чтобы не нагружать машину с 2 ГБ памяти
    maxHeapSize = "256m"
    maxParallelForks = 1
    systemProperty("mtp.test.key", (project.findProperty("mtp.test.key") as String?) ?: "")
    systemProperty("mtp.test.ssh", (project.findProperty("mtp.test.ssh") as String?) ?: "")
}
