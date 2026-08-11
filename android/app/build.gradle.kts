import org.gradle.api.file.DirectoryProperty
import org.gradle.api.tasks.OutputDirectory
import org.gradle.api.tasks.Sync

plugins {
    id("com.android.application")
}

abstract class PrepareEcsAssets : Sync() {
    @get:OutputDirectory
    abstract val outputDirectory: DirectoryProperty

    init {
        into(outputDirectory)
    }
}

val repositoryRoot = rootProject.projectDir.parentFile
val generatedAssets = layout.buildDirectory.dir("generated/ecsAssets")
val defaultEcsConfig = layout.projectDirectory.file("src/main/ecs/config.json").asFile
val releaseStorePath = providers.environmentVariable("ECS_ANDROID_KEYSTORE").orNull
val releaseStorePassword = providers.environmentVariable("ECS_ANDROID_STORE_PASSWORD").orNull
val releaseKeyAlias = providers.environmentVariable("ECS_ANDROID_KEY_ALIAS").orNull
val releaseKeyPassword = providers.environmentVariable("ECS_ANDROID_KEY_PASSWORD").orNull
val releaseSigningReady = listOf(
    releaseStorePath,
    releaseStorePassword,
    releaseKeyAlias,
    releaseKeyPassword,
).all { !it.isNullOrBlank() }

val prepareEcsAssets by tasks.registering(PrepareEcsAssets::class) {
    outputDirectory.set(generatedAssets)
    from(repositoryRoot.resolve("prompts")) { into("prompts") }
    from(repositoryRoot.resolve("agents.d")) { into("agents.d") }
    from(repositoryRoot.resolve("pipelines.d")) { into("pipelines.d") }
    from(repositoryRoot.resolve("tools.d")) { into("tools.d") }
    from(repositoryRoot.resolve("skills.d")) { into("skills.d") }
    from(repositoryRoot.resolve("office.d")) { into("office.d") }
    from(defaultEcsConfig) {
        rename { "config.json" }
    }
}

val buildEcsMobileAar by tasks.registering(Exec::class) {
    workingDir(repositoryRoot)
    commandLine("make", "mobile-aar")
    inputs.files(
        fileTree(repositoryRoot.resolve("mobile")) { include("**/*.go") },
        fileTree(repositoryRoot.resolve("internal")) { include("**/*.go") },
        fileTree(repositoryRoot.resolve("internal/adminui/assets")) { include("**/*") },
        repositoryRoot.resolve("go.mod"),
        repositoryRoot.resolve("go.sum"),
        repositoryRoot.resolve("Makefile"),
        repositoryRoot.resolve("scripts/build-mobile-aar.sh"),
    )
    outputs.file(layout.projectDirectory.file("libs/ecs-mobile.aar"))
}

android {
    namespace = "io.github.yourblacksky.ecs"
    compileSdk = 36

    defaultConfig {
        applicationId = "io.github.yourblacksky.ecs"
        minSdk = 26
        targetSdk = 36
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        versionCode = providers.environmentVariable("ECS_ANDROID_VERSION_CODE")
            .orNull?.toIntOrNull() ?: 1
        versionName = providers.environmentVariable("ECS_ANDROID_VERSION_NAME")
            .orNull ?: "0.1.0"
    }

    signingConfigs {
        if (releaseSigningReady) {
            create("ecsRelease") {
                storeFile = file(releaseStorePath!!)
                storePassword = releaseStorePassword
                keyAlias = releaseKeyAlias
                keyPassword = releaseKeyPassword
            }
        }
    }

    splits {
        abi {
            isEnable = true
            reset()
            include("armeabi-v7a", "arm64-v8a", "x86_64")
            isUniversalApk = true
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            if (releaseSigningReady) {
                signingConfig = signingConfigs.getByName("ecsRelease")
            }
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
        }
    }

    testOptions {
        animationsDisabled = true
    }
}

androidComponents {
    onVariants { variant ->
        variant.sources.assets?.addGeneratedSourceDirectory(
            prepareEcsAssets,
            PrepareEcsAssets::outputDirectory,
        )
    }
}

tasks.named("preBuild") {
    dependsOn(buildEcsMobileAar)
}

dependencies {
    implementation(files("libs/ecs-mobile.aar"))
    androidTestImplementation("androidx.test:runner:1.7.0")
    androidTestImplementation("androidx.test:rules:1.7.0")
    androidTestImplementation("androidx.test.ext:junit:1.3.0")
    androidTestImplementation("androidx.test.espresso:espresso-core:3.7.0")
}
