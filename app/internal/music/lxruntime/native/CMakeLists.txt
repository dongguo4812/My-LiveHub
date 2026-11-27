cmake_minimum_required(VERSION 3.20)
project(lxqjs_native C)

set(CMAKE_C_STANDARD 11)
set(CMAKE_C_STANDARD_REQUIRED ON)

if(NOT DEFINED QUICKJS_NG_SOURCE_DIR)
    message(FATAL_ERROR "QUICKJS_NG_SOURCE_DIR must point to the pinned QuickJS-NG source tree")
endif()

add_subdirectory("${QUICKJS_NG_SOURCE_DIR}" "${CMAKE_BINARY_DIR}/quickjs-ng")
set_target_properties(qjs PROPERTIES ARCHIVE_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/lib" POSITION_INDEPENDENT_CODE ON)

add_library(lxqjs SHARED "${CMAKE_CURRENT_LIST_DIR}/lxqjs.c")
target_include_directories(lxqjs PRIVATE "${CMAKE_CURRENT_LIST_DIR}/include" "${QUICKJS_NG_SOURCE_DIR}")
target_link_libraries(lxqjs PRIVATE qjs)
if(MINGW)
    target_link_options(lxqjs PRIVATE -static -static-libgcc -static-libstdc++)
endif()
set_target_properties(lxqjs PROPERTIES PREFIX "" OUTPUT_NAME "lxqjs" WINDOWS_EXPORT_ALL_SYMBOLS ON
    RUNTIME_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/runtime"
    LIBRARY_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/runtime")
