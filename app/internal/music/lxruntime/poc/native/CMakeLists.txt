cmake_minimum_required(VERSION 3.20)
project(lx_quickjs_host_smoke C)

set(CMAKE_C_STANDARD 11)
set(CMAKE_C_STANDARD_REQUIRED ON)

if(NOT DEFINED QUICKJS_NG_SOURCE_DIR)
    message(FATAL_ERROR "QUICKJS_NG_SOURCE_DIR must point to the pinned QuickJS-NG source tree")
endif()

add_subdirectory("${QUICKJS_NG_SOURCE_DIR}" "${CMAKE_BINARY_DIR}/quickjs-ng")
set_target_properties(qjs PROPERTIES ARCHIVE_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/lib")

add_executable(lx_quickjs_host_smoke main.c)
target_include_directories(lx_quickjs_host_smoke PRIVATE "${QUICKJS_NG_SOURCE_DIR}")
target_link_libraries(lx_quickjs_host_smoke PRIVATE qjs)
