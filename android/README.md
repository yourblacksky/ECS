# ECS Android

This host packages the shared Go `mobile` facade in an Android foreground
service. It can run as a loopback-only node or advertise itself on the local
network, discover ECS peers through Android NSD and the ECS UDP announcement
protocol, and automatically submit a parent enrollment request.

## Build

Requirements:

- JDK 17 or newer
- Android SDK 36
- Android NDK installed through the SDK manager
- Go

From the repository root:

```bash
make mobile-aar
cd android
./gradlew assembleDebug
```

The Gradle `preBuild` task regenerates the AAR and synchronizes the prompts,
agents, pipelines, tools, and skills into APK assets. ABI-specific APKs are
generated for `armeabi-v7a`, `arm64-v8a`, and `x86_64`; the universal APK is
intended only for convenient testing.

The settings dialog stores the LLM API key with Android Keystore. `config.json`
contains only `api_key_secret` and `admin_token_secret` references; the values
are passed to Go in memory and are not persisted in ECS configuration.

The Android host registers six platform-limited device Skills through one
`DeviceCapabilityProvider`: flashlight, vibration, beep, screen brightness,
local notification, and one-shot location. Camera, notification, and location
use Android runtime permissions. System-wide brightness changes additionally
require the user to tap **授权屏幕亮度控制** in the node settings. ECS always
turns off the torch and cancels vibration when the service stops.

Every Android build packages the tracked credential-free configuration. Model
credentials are entered after installation and stored with Android Keystore;
there is no Gradle property or environment-variable path for embedding an API
key in an APK.

Managed assets use a hash manifest during upgrades. Unmodified packaged files
receive updates, while files edited through the ECS admin remain untouched.

## UI tests

Start an Android 13 or newer emulator, then run:

```bash
./scripts/test-android-ui.sh
```

The Espresso suite verifies runtime start/stop and health, the embedded admin
WebView, credential-copy feedback, and settings save/restore. It grants only
the notification and nearby-device permissions required by the test app. The
script also works with one authorized physical device and installs only its ABI
split. `./gradlew connectedDebugAndroidTest` remains available for standard
Gradle/CI device orchestration.

## Release bundle

Set signing values outside the repository:

```bash
export ECS_ANDROID_KEYSTORE=/absolute/path/release.jks
export ECS_ANDROID_STORE_PASSWORD=...
export ECS_ANDROID_KEY_ALIAS=...
export ECS_ANDROID_KEY_PASSWORD=...
export ECS_ANDROID_VERSION_CODE=2
export ECS_ANDROID_VERSION_NAME=0.2.0
./gradlew bundleRelease
```

The application never writes these signing values into Gradle files.
