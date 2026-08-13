#include "guicontroller.h"

#include <QGuiApplication>
#include <QQmlApplicationEngine>

int main(int argc, char *argv[])
{
    QGuiApplication app(argc, argv);
    QGuiApplication::setApplicationDisplayName(QStringLiteral("cliamp GUI"));

    QQmlApplicationEngine engine;
    engine.loadFromModule("CliampGui", "Main");

    if (engine.rootObjects().isEmpty()) {
        return 1;
    }
    return app.exec();
}
