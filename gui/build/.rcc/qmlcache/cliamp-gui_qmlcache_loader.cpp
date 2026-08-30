#include <QtQml/qqmlprivate.h>
#include <QtCore/qdir.h>
#include <QtCore/qurl.h>
#include <QtCore/qhash.h>
#include <QtCore/qstring.h>

namespace QmlCacheGeneratedCode {
namespace _qt_qml_CliampGui_qml_Main_qml { 
    extern const unsigned char qmlData[];
    extern const QQmlPrivate::AOTCompiledFunction aotBuiltFunctions[];
    const QQmlPrivate::CachedQmlUnit unit = {
        reinterpret_cast<const QV4::CompiledData::Unit*>(&qmlData), &aotBuiltFunctions[0], nullptr
    };
}
namespace _qt_qml_CliampGui_qml_Theme_qml { 
    extern const unsigned char qmlData[];
    extern const QQmlPrivate::AOTCompiledFunction aotBuiltFunctions[];
    const QQmlPrivate::CachedQmlUnit unit = {
        reinterpret_cast<const QV4::CompiledData::Unit*>(&qmlData), &aotBuiltFunctions[0], nullptr
    };
}
namespace _qt_qml_CliampGui_qml_FlatButton_qml { 
    extern const unsigned char qmlData[];
    extern const QQmlPrivate::AOTCompiledFunction aotBuiltFunctions[];
    const QQmlPrivate::CachedQmlUnit unit = {
        reinterpret_cast<const QV4::CompiledData::Unit*>(&qmlData), &aotBuiltFunctions[0], nullptr
    };
}
namespace _qt_qml_CliampGui_qml_AlbumArt_qml { 
    extern const unsigned char qmlData[];
    extern const QQmlPrivate::AOTCompiledFunction aotBuiltFunctions[];
    const QQmlPrivate::CachedQmlUnit unit = {
        reinterpret_cast<const QV4::CompiledData::Unit*>(&qmlData), &aotBuiltFunctions[0], nullptr
    };
}
namespace _qt_qml_CliampGui_qml_SpectrumVisualizer_qml { 
    extern const unsigned char qmlData[];
    extern const QQmlPrivate::AOTCompiledFunction aotBuiltFunctions[];
    const QQmlPrivate::CachedQmlUnit unit = {
        reinterpret_cast<const QV4::CompiledData::Unit*>(&qmlData), &aotBuiltFunctions[0], nullptr
    };
}
namespace _qt_qml_CliampGui_qml_PlaylistView_qml { 
    extern const unsigned char qmlData[];
    extern const QQmlPrivate::AOTCompiledFunction aotBuiltFunctions[];
    const QQmlPrivate::CachedQmlUnit unit = {
        reinterpret_cast<const QV4::CompiledData::Unit*>(&qmlData), &aotBuiltFunctions[0], nullptr
    };
}
namespace _qt_qml_CliampGui_qml_EqualizerView_qml { 
    extern const unsigned char qmlData[];
    extern const QQmlPrivate::AOTCompiledFunction aotBuiltFunctions[];
    const QQmlPrivate::CachedQmlUnit unit = {
        reinterpret_cast<const QV4::CompiledData::Unit*>(&qmlData), &aotBuiltFunctions[0], nullptr
    };
}
namespace _qt_qml_CliampGui_qml_LibraryView_qml { 
    extern const unsigned char qmlData[];
    extern const QQmlPrivate::AOTCompiledFunction aotBuiltFunctions[];
    const QQmlPrivate::CachedQmlUnit unit = {
        reinterpret_cast<const QV4::CompiledData::Unit*>(&qmlData), &aotBuiltFunctions[0], nullptr
    };
}
namespace _qt_qml_CliampGui_qml_RadioView_qml { 
    extern const unsigned char qmlData[];
    extern const QQmlPrivate::AOTCompiledFunction aotBuiltFunctions[];
    const QQmlPrivate::CachedQmlUnit unit = {
        reinterpret_cast<const QV4::CompiledData::Unit*>(&qmlData), &aotBuiltFunctions[0], nullptr
    };
}

}
namespace {
struct Registry {
    Registry();
    ~Registry();
    QHash<QString, const QQmlPrivate::CachedQmlUnit*> resourcePathToCachedUnit;
    static const QQmlPrivate::CachedQmlUnit *lookupCachedUnit(const QUrl &url);
};

Q_GLOBAL_STATIC(Registry, unitRegistry)


Registry::Registry() {
    resourcePathToCachedUnit.insert(QStringLiteral("/qt/qml/CliampGui/qml/Main.qml"), &QmlCacheGeneratedCode::_qt_qml_CliampGui_qml_Main_qml::unit);
    resourcePathToCachedUnit.insert(QStringLiteral("/qt/qml/CliampGui/qml/Theme.qml"), &QmlCacheGeneratedCode::_qt_qml_CliampGui_qml_Theme_qml::unit);
    resourcePathToCachedUnit.insert(QStringLiteral("/qt/qml/CliampGui/qml/FlatButton.qml"), &QmlCacheGeneratedCode::_qt_qml_CliampGui_qml_FlatButton_qml::unit);
    resourcePathToCachedUnit.insert(QStringLiteral("/qt/qml/CliampGui/qml/AlbumArt.qml"), &QmlCacheGeneratedCode::_qt_qml_CliampGui_qml_AlbumArt_qml::unit);
    resourcePathToCachedUnit.insert(QStringLiteral("/qt/qml/CliampGui/qml/SpectrumVisualizer.qml"), &QmlCacheGeneratedCode::_qt_qml_CliampGui_qml_SpectrumVisualizer_qml::unit);
    resourcePathToCachedUnit.insert(QStringLiteral("/qt/qml/CliampGui/qml/PlaylistView.qml"), &QmlCacheGeneratedCode::_qt_qml_CliampGui_qml_PlaylistView_qml::unit);
    resourcePathToCachedUnit.insert(QStringLiteral("/qt/qml/CliampGui/qml/EqualizerView.qml"), &QmlCacheGeneratedCode::_qt_qml_CliampGui_qml_EqualizerView_qml::unit);
    resourcePathToCachedUnit.insert(QStringLiteral("/qt/qml/CliampGui/qml/LibraryView.qml"), &QmlCacheGeneratedCode::_qt_qml_CliampGui_qml_LibraryView_qml::unit);
    resourcePathToCachedUnit.insert(QStringLiteral("/qt/qml/CliampGui/qml/RadioView.qml"), &QmlCacheGeneratedCode::_qt_qml_CliampGui_qml_RadioView_qml::unit);
    QQmlPrivate::RegisterQmlUnitCacheHook registration;
    registration.structVersion = 0;
    registration.lookupCachedQmlUnit = &lookupCachedUnit;
    QQmlPrivate::qmlregister(QQmlPrivate::QmlUnitCacheHookRegistration, &registration);
}

Registry::~Registry() {
    QQmlPrivate::qmlunregister(QQmlPrivate::QmlUnitCacheHookRegistration, quintptr(&lookupCachedUnit));
}

const QQmlPrivate::CachedQmlUnit *Registry::lookupCachedUnit(const QUrl &url) {
    if (url.scheme() != QLatin1String("qrc"))
        return nullptr;
    QString resourcePath = QDir::cleanPath(url.path());
    if (resourcePath.isEmpty())
        return nullptr;
    if (!resourcePath.startsWith(QLatin1Char('/')))
        resourcePath.prepend(QLatin1Char('/'));
    return unitRegistry()->resourcePathToCachedUnit.value(resourcePath, nullptr);
}
}
int QT_MANGLE_NAMESPACE(qInitResources_qmlcache_cliamp_gui)() {
    ::unitRegistry();
    return 1;
}
Q_CONSTRUCTOR_FUNCTION(QT_MANGLE_NAMESPACE(qInitResources_qmlcache_cliamp_gui))
int QT_MANGLE_NAMESPACE(qCleanupResources_qmlcache_cliamp_gui)() {
    return 1;
}
