cmake_minimum_required(VERSION 3.16)
list(APPEND EXTRA_COMPONENT_DIRS "${CMAKE_CURRENT_SOURCE_DIR}/../../components")
set(EXCLUDE_COMPONENTS openthread_manager)

include($ENV{IDF_PATH}/tools/cmake/project.cmake)
project(garage_door_opener)
