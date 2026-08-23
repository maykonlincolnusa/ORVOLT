cmake_minimum_required(VERSION 3.24)
project(orvolt_evse_simulator VERSION 0.1.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

include(FetchContent)
set(PAHO_BUILD_STATIC ON CACHE BOOL "" FORCE)
set(PAHO_BUILD_SHARED OFF CACHE BOOL "" FORCE)
set(PAHO_WITH_SSL OFF CACHE BOOL "" FORCE)
set(PAHO_ENABLE_TESTING OFF CACHE BOOL "" FORCE)
FetchContent_Declare(
  paho_mqtt_c
  GIT_REPOSITORY https://github.com/eclipse-paho/paho.mqtt.c.git
  GIT_TAG v1.3.14
  GIT_SHALLOW TRUE
)
FetchContent_MakeAvailable(paho_mqtt_c)

add_library(evse_telemetry src/telemetry.cpp)
target_include_directories(evse_telemetry PUBLIC include)
target_compile_options(evse_telemetry PRIVATE $<$<CXX_COMPILER_ID:MSVC>:/W4 /WX> $<$<NOT:$<CXX_COMPILER_ID:MSVC>>:-Wall -Wextra -Wpedantic -Werror>)

add_executable(evse-simulator src/main.cpp)
target_link_libraries(evse-simulator PRIVATE evse_telemetry paho-mqtt3a-static)
target_compile_options(evse-simulator PRIVATE $<$<CXX_COMPILER_ID:MSVC>:/W4 /wd4201> $<$<NOT:$<CXX_COMPILER_ID:MSVC>>:-Wall -Wextra -Wpedantic>)

include(CTest)
if(BUILD_TESTING)
  add_executable(evse-telemetry-test tests/telemetry_test.cpp)
  target_link_libraries(evse-telemetry-test PRIVATE evse_telemetry)
  add_test(NAME evse-telemetry-test COMMAND evse-telemetry-test)
endif()
