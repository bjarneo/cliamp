cmake_minimum_required(VERSION 3.21)

project(cliamp_gui
    VERSION 0.1.0
    LANGUAGES CXX
)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

find_package(Qt6 6.8 REQUIRED COMPONENTS Quick Network)

qt_standard_project_setup(REQUIRES 6.8)

qt_add_executable(cliamp-gui
    src/main.cpp
    src/guicontroller.cpp
    src/guicontroller.h
    src/ipcclient.cpp
    src/ipcclient.h
    src/listmodel.cpp
    src/listmodel.h
)

set_source_files_properties(qml/Theme.qml PROPERTIES
    QT_QML_SINGLETON_TYPE TRUE
)

qt_add_qml_module(cliamp-gui
    URI CliampGui
    SOURCES
        src/guicontroller.h
    QML_FILES
        qml/Main.qml
        qml/Theme.qml
        qml/LedButton.qml
        qml/SpectrumVisualizer.qml
        qml/PlaylistView.qml
        qml/EqualizerView.qml
        qml/LibraryView.qml
        qml/RadioView.qml
)

target_link_libraries(cliamp-gui
    PRIVATE
        Qt6::Network
        Qt6::QmlIntegration
        Qt6::Quick
)

target_include_directories(cliamp-gui
    PRIVATE
        ${CMAKE_CURRENT_SOURCE_DIR}/src
)
