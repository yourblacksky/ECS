cmake_minimum_required(VERSION 3.5.0)
project(ecs_harmony)

set(ECS_NATIVE_ROOT ${CMAKE_CURRENT_SOURCE_DIR})

add_library(ecs_harmony SHARED napi_init.cpp)
target_include_directories(ecs_harmony PRIVATE ${ECS_NATIVE_ROOT}/include)
target_link_libraries(ecs_harmony PUBLIC libace_napi.z.so libhilog_ndk.z.so libdl.so)
add_custom_command(
    TARGET ecs_harmony
    POST_BUILD
    COMMAND ${CMAKE_COMMAND} -E copy_if_different
        ${ECS_NATIVE_ROOT}/../libs/${CMAKE_OHOS_ARCH_ABI}/libecs_go.so
        $<TARGET_FILE_DIR:ecs_harmony>/libecs_go.so
)
