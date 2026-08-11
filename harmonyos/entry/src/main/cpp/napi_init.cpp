#include "napi/native_api.h"

#include <dlfcn.h>
#include <hilog/log.h>
#include <string>
#include <vector>

using StartFunction = char *(*)(char *, char *);
using StopFunction = char *(*)();
using StatusFunction = char *(*)();
using SetLLMAPIKeyFunction = char *(*)(char *);
using DiscoveryAnnouncementFunction = char *(*)();
using PushDiscoveryAnnouncementFunction = char *(*)(char *);
using FreeFunction = void (*)(char *);

static void *runtimeHandle = nullptr;
static StartFunction runtimeStart = nullptr;
static StopFunction runtimeStop = nullptr;
static StatusFunction runtimeStatus = nullptr;
static SetLLMAPIKeyFunction runtimeSetLLMAPIKey = nullptr;
static DiscoveryAnnouncementFunction runtimeDiscoveryAnnouncement = nullptr;
static PushDiscoveryAnnouncementFunction runtimePushDiscoveryAnnouncement = nullptr;
static FreeFunction runtimeFree = nullptr;

static std::string ReadString(napi_env env, napi_value value)
{
    size_t length = 0;
    napi_get_value_string_utf8(env, value, nullptr, 0, &length);
    std::vector<char> buffer(length + 1);
    napi_get_value_string_utf8(env, value, buffer.data(), buffer.size(), &length);
    return std::string(buffer.data(), length);
}

static napi_value MakeString(napi_env env, const char *text)
{
    napi_value result;
    napi_create_string_utf8(env, text == nullptr ? "" : text, NAPI_AUTO_LENGTH, &result);
    return result;
}

static napi_value CopyRuntimeString(napi_env env, char *value)
{
    napi_value result = MakeString(env, value);
    if (value != nullptr && runtimeFree != nullptr) {
        runtimeFree(value);
    }
    return result;
}

static std::string EnsureRuntime()
{
    if (runtimeHandle != nullptr) {
        return "";
    }
    // Keep Go out of the HAP's eager dependency graph. ArkUI imports this
    // N-API module while constructing the page; loading the runtime here lets
    // its scheduler start after the application process and UI are ready.
    runtimeHandle = dlopen("libecs_go.so", RTLD_NOW | RTLD_GLOBAL);
    if (runtimeHandle == nullptr) {
        return dlerror();
    }
    runtimeStart = reinterpret_cast<StartFunction>(dlsym(runtimeHandle, "ECSHarmonyStart"));
    runtimeStop = reinterpret_cast<StopFunction>(dlsym(runtimeHandle, "ECSHarmonyStop"));
    runtimeStatus = reinterpret_cast<StatusFunction>(dlsym(runtimeHandle, "ECSHarmonyStatusJSON"));
    runtimeSetLLMAPIKey =
        reinterpret_cast<SetLLMAPIKeyFunction>(dlsym(runtimeHandle, "ECSHarmonySetLLMAPIKey"));
    runtimeDiscoveryAnnouncement =
        reinterpret_cast<DiscoveryAnnouncementFunction>(dlsym(runtimeHandle, "ECSHarmonyDiscoveryAnnouncement"));
    runtimePushDiscoveryAnnouncement =
        reinterpret_cast<PushDiscoveryAnnouncementFunction>(
            dlsym(runtimeHandle, "ECSHarmonyPushDiscoveryAnnouncement"));
    runtimeFree = reinterpret_cast<FreeFunction>(dlsym(runtimeHandle, "ECSHarmonyFree"));
    if (runtimeStart == nullptr || runtimeStop == nullptr || runtimeStatus == nullptr ||
        runtimeSetLLMAPIKey == nullptr || runtimeDiscoveryAnnouncement == nullptr ||
        runtimePushDiscoveryAnnouncement == nullptr || runtimeFree == nullptr) {
        return "ECS Go runtime exports are incomplete";
    }
    return "";
}

static napi_value Start(napi_env env, napi_callback_info info)
{
    size_t argc = 2;
    napi_value args[2] = {nullptr, nullptr};
    napi_get_cb_info(env, info, &argc, args, nullptr, nullptr);
    if (argc < 1) {
        napi_throw_type_error(env, nullptr, "dataDir is required");
        return nullptr;
    }
    std::string dataDir = ReadString(env, args[0]);
    std::string loadError = EnsureRuntime();
    if (!loadError.empty()) {
        return MakeString(env, loadError.c_str());
    }
    std::string secrets = argc >= 2 ? ReadString(env, args[1]) : "{}";
    OH_LOG_Print(LOG_APP, LOG_INFO, 0x4543, "ECSNative", "starting embedded Go runtime");
    // The OpenHarmony Go fork cannot safely re-enter a running c-shared
    // runtime on the same ArkUI thread. This one startup result intentionally
    // remains allocated for the process lifetime instead of making a second
    // exported call solely to release a few hundred bytes.
    return MakeString(env, runtimeStart(
        const_cast<char *>(dataDir.c_str()),
        const_cast<char *>(secrets.c_str())
    ));
}

static napi_value SetLLMAPIKey(napi_env env, napi_callback_info info)
{
    size_t argc = 1;
    napi_value args[1] = {nullptr};
    napi_get_cb_info(env, info, &argc, args, nullptr, nullptr);
    if (argc < 1) {
        napi_throw_type_error(env, nullptr, "apiKey is required");
        return nullptr;
    }
    std::string loadError = EnsureRuntime();
    if (!loadError.empty()) {
        return MakeString(env, loadError.c_str());
    }
    std::string apiKey = ReadString(env, args[0]);
    // See Start: keep this small one-shot return value for the process
    // lifetime instead of re-entering the Go shared runtime only to free it.
    return MakeString(env, runtimeSetLLMAPIKey(const_cast<char *>(apiKey.c_str())));
}

static napi_value Stop(napi_env env, napi_callback_info info)
{
    if (runtimeHandle == nullptr) {
        return MakeString(env, "");
    }
    return CopyRuntimeString(env, runtimeStop());
}

static napi_value StatusJSON(napi_env env, napi_callback_info info)
{
    if (runtimeHandle == nullptr) {
        return MakeString(env, "{\"running\":false}");
    }
    return CopyRuntimeString(env, runtimeStatus());
}

static napi_value DiscoveryAnnouncement(napi_env env, napi_callback_info info)
{
    std::string loadError = EnsureRuntime();
    if (!loadError.empty()) {
        return MakeString(env, "");
    }
    return CopyRuntimeString(env, runtimeDiscoveryAnnouncement());
}

static napi_value PushDiscoveryAnnouncement(napi_env env, napi_callback_info info)
{
    size_t argc = 1;
    napi_value args[1] = {nullptr};
    napi_get_cb_info(env, info, &argc, args, nullptr, nullptr);
    if (argc < 1) {
        napi_throw_type_error(env, nullptr, "discovery announcement is required");
        return nullptr;
    }
    std::string loadError = EnsureRuntime();
    if (!loadError.empty()) {
        return MakeString(env, loadError.c_str());
    }
    std::string payload = ReadString(env, args[0]);
    return CopyRuntimeString(
        env,
        runtimePushDiscoveryAnnouncement(const_cast<char *>(payload.c_str()))
    );
}

EXTERN_C_START
static napi_value Init(napi_env env, napi_value exports)
{
    napi_property_descriptor descriptors[] = {
        {"start", nullptr, Start, nullptr, nullptr, nullptr, napi_default, nullptr},
        {"stop", nullptr, Stop, nullptr, nullptr, nullptr, napi_default, nullptr},
        {"statusJSON", nullptr, StatusJSON, nullptr, nullptr, nullptr, napi_default, nullptr},
        {"setLLMAPIKey", nullptr, SetLLMAPIKey, nullptr, nullptr, nullptr, napi_default, nullptr},
        {"discoveryAnnouncement", nullptr, DiscoveryAnnouncement, nullptr, nullptr, nullptr,
         napi_default, nullptr},
        {"pushDiscoveryAnnouncement", nullptr, PushDiscoveryAnnouncement, nullptr, nullptr, nullptr,
         napi_default, nullptr},
    };
    napi_define_properties(env, exports, sizeof(descriptors) / sizeof(descriptors[0]), descriptors);
    return exports;
}
EXTERN_C_END

static napi_module ecsModule = {
    .nm_version = 1,
    .nm_flags = 0,
    .nm_filename = nullptr,
    .nm_register_func = Init,
    .nm_modname = "ecs_harmony",
    .nm_priv = nullptr,
    .reserved = {0},
};

extern "C" __attribute__((constructor)) void RegisterECSHarmonyModule(void)
{
    napi_module_register(&ecsModule);
}
