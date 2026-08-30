/****************************************************************************
** Meta object code from reading C++ file 'guicontroller.h'
**
** Created by: The Qt Meta Object Compiler version 69 (Qt 6.11.2)
**
** WARNING! All changes made in this file will be lost!
*****************************************************************************/

#include "../../../src/guicontroller.h"
#include <QtCore/qmetatype.h>

#include <QtCore/qtmochelpers.h>

#include <memory>


#include <QtCore/qxptype_traits.h>
#if !defined(Q_MOC_OUTPUT_REVISION)
#error "The header file 'guicontroller.h' doesn't include <QObject>."
#elif Q_MOC_OUTPUT_REVISION != 69
#error "This file was generated using the moc from 6.11.2. It"
#error "cannot be used with the include files from this version of Qt."
#error "(The moc has changed too much.)"
#endif

#ifndef Q_CONSTINIT
#define Q_CONSTINIT
#endif

QT_WARNING_PUSH
QT_WARNING_DISABLE_DEPRECATED
QT_WARNING_DISABLE_GCC("-Wuseless-cast")
namespace {
struct qt_meta_tag_ZN13GuiControllerE_t {};
} // unnamed namespace

template <> constexpr inline auto GuiController::qt_create_metaobjectdata<qt_meta_tag_ZN13GuiControllerE_t>()
{
    namespace QMC = QtMocConstants;
    QtMocHelpers::StringRefStorage qt_stringData {
        "GuiController",
        "QML.Element",
        "auto",
        "trackChanged",
        "",
        "stateChanged",
        "positionChanged",
        "volumeChanged",
        "playbackModesChanged",
        "equalizerChanged",
        "eqPresetsChanged",
        "queueChanged",
        "connectionChanged",
        "selectedProviderChanged",
        "selectedPlaylistChanged",
        "desktopThemeChanged",
        "setupSpecsChanged",
        "setupStateChanged",
        "bandsChanged",
        "QVariantList",
        "bands",
        "startDaemon",
        "toggle",
        "stop",
        "next",
        "previous",
        "seekTo",
        "seconds",
        "setVolume",
        "db",
        "setShuffle",
        "enabled",
        "cycleRepeat",
        "setEqBand",
        "band",
        "gain",
        "setEqPreset",
        "preset",
        "playQueue",
        "index",
        "clearQueue",
        "selectProvider",
        "key",
        "browseProviderPlaylist",
        "playlist",
        "loadSelectedPlaylist",
        "playLibraryTrack",
        "searchProvider",
        "query",
        "loadRadioStation",
        "station",
        "formatDuration",
        "loadSetupSpecs",
        "connectProvider",
        "QVariantMap",
        "values",
        "force",
        "clearSetupResult",
        "title",
        "artist",
        "album",
        "albumArtUrl",
        "state",
        "format",
        "sourceLabel",
        "audioDevice",
        "seekable",
        "position",
        "duration",
        "volume",
        "shuffle",
        "repeat",
        "mono",
        "speed",
        "eqPreset",
        "eqBands",
        "eqPresets",
        "currentIndex",
        "queueCount",
        "connected",
        "connectionMessage",
        "selectedProvider",
        "selectedProviderSearchable",
        "selectedPlaylist",
        "desktopTheme",
        "omarchySession",
        "setupSpecs",
        "setupBusy",
        "setupMessage",
        "setupFailed",
        "setupCanForce",
        "queueModel",
        "QAbstractItemModel*",
        "providersModel",
        "providerPlaylistsModel",
        "libraryTracksModel",
        "radioModel"
    };

    QtMocHelpers::UintData qt_methods {
        // Signal 'trackChanged'
        QtMocHelpers::SignalData<void()>(3, 4, QMC::AccessPublic, QMetaType::Void),
        // Signal 'stateChanged'
        QtMocHelpers::SignalData<void()>(5, 4, QMC::AccessPublic, QMetaType::Void),
        // Signal 'positionChanged'
        QtMocHelpers::SignalData<void()>(6, 4, QMC::AccessPublic, QMetaType::Void),
        // Signal 'volumeChanged'
        QtMocHelpers::SignalData<void()>(7, 4, QMC::AccessPublic, QMetaType::Void),
        // Signal 'playbackModesChanged'
        QtMocHelpers::SignalData<void()>(8, 4, QMC::AccessPublic, QMetaType::Void),
        // Signal 'equalizerChanged'
        QtMocHelpers::SignalData<void()>(9, 4, QMC::AccessPublic, QMetaType::Void),
        // Signal 'eqPresetsChanged'
        QtMocHelpers::SignalData<void()>(10, 4, QMC::AccessPublic, QMetaType::Void),
        // Signal 'queueChanged'
        QtMocHelpers::SignalData<void()>(11, 4, QMC::AccessPublic, QMetaType::Void),
        // Signal 'connectionChanged'
        QtMocHelpers::SignalData<void()>(12, 4, QMC::AccessPublic, QMetaType::Void),
        // Signal 'selectedProviderChanged'
        QtMocHelpers::SignalData<void()>(13, 4, QMC::AccessPublic, QMetaType::Void),
        // Signal 'selectedPlaylistChanged'
        QtMocHelpers::SignalData<void()>(14, 4, QMC::AccessPublic, QMetaType::Void),
        // Signal 'desktopThemeChanged'
        QtMocHelpers::SignalData<void()>(15, 4, QMC::AccessPublic, QMetaType::Void),
        // Signal 'setupSpecsChanged'
        QtMocHelpers::SignalData<void()>(16, 4, QMC::AccessPublic, QMetaType::Void),
        // Signal 'setupStateChanged'
        QtMocHelpers::SignalData<void()>(17, 4, QMC::AccessPublic, QMetaType::Void),
        // Signal 'bandsChanged'
        QtMocHelpers::SignalData<void(const QVariantList &)>(18, 4, QMC::AccessPublic, QMetaType::Void, {{
            { 0x80000000 | 19, 20 },
        }}),
        // Method 'startDaemon'
        QtMocHelpers::MethodData<void()>(21, 4, QMC::AccessPublic, QMetaType::Void),
        // Method 'toggle'
        QtMocHelpers::MethodData<void()>(22, 4, QMC::AccessPublic, QMetaType::Void),
        // Method 'stop'
        QtMocHelpers::MethodData<void()>(23, 4, QMC::AccessPublic, QMetaType::Void),
        // Method 'next'
        QtMocHelpers::MethodData<void()>(24, 4, QMC::AccessPublic, QMetaType::Void),
        // Method 'previous'
        QtMocHelpers::MethodData<void()>(25, 4, QMC::AccessPublic, QMetaType::Void),
        // Method 'seekTo'
        QtMocHelpers::MethodData<void(double)>(26, 4, QMC::AccessPublic, QMetaType::Void, {{
            { QMetaType::Double, 27 },
        }}),
        // Method 'setVolume'
        QtMocHelpers::MethodData<void(double)>(28, 4, QMC::AccessPublic, QMetaType::Void, {{
            { QMetaType::Double, 29 },
        }}),
        // Method 'setShuffle'
        QtMocHelpers::MethodData<void(bool)>(30, 4, QMC::AccessPublic, QMetaType::Void, {{
            { QMetaType::Bool, 31 },
        }}),
        // Method 'cycleRepeat'
        QtMocHelpers::MethodData<void()>(32, 4, QMC::AccessPublic, QMetaType::Void),
        // Method 'setEqBand'
        QtMocHelpers::MethodData<void(int, double)>(33, 4, QMC::AccessPublic, QMetaType::Void, {{
            { QMetaType::Int, 34 }, { QMetaType::Double, 35 },
        }}),
        // Method 'setEqPreset'
        QtMocHelpers::MethodData<void(const QString &)>(36, 4, QMC::AccessPublic, QMetaType::Void, {{
            { QMetaType::QString, 37 },
        }}),
        // Method 'playQueue'
        QtMocHelpers::MethodData<void(int)>(38, 4, QMC::AccessPublic, QMetaType::Void, {{
            { QMetaType::Int, 39 },
        }}),
        // Method 'clearQueue'
        QtMocHelpers::MethodData<void()>(40, 4, QMC::AccessPublic, QMetaType::Void),
        // Method 'selectProvider'
        QtMocHelpers::MethodData<void(const QString &)>(41, 4, QMC::AccessPublic, QMetaType::Void, {{
            { QMetaType::QString, 42 },
        }}),
        // Method 'browseProviderPlaylist'
        QtMocHelpers::MethodData<void(const QString &)>(43, 4, QMC::AccessPublic, QMetaType::Void, {{
            { QMetaType::QString, 44 },
        }}),
        // Method 'loadSelectedPlaylist'
        QtMocHelpers::MethodData<void()>(45, 4, QMC::AccessPublic, QMetaType::Void),
        // Method 'playLibraryTrack'
        QtMocHelpers::MethodData<void(int)>(46, 4, QMC::AccessPublic, QMetaType::Void, {{
            { QMetaType::Int, 39 },
        }}),
        // Method 'searchProvider'
        QtMocHelpers::MethodData<void(const QString &)>(47, 4, QMC::AccessPublic, QMetaType::Void, {{
            { QMetaType::QString, 48 },
        }}),
        // Method 'loadRadioStation'
        QtMocHelpers::MethodData<void(const QString &)>(49, 4, QMC::AccessPublic, QMetaType::Void, {{
            { QMetaType::QString, 50 },
        }}),
        // Method 'formatDuration'
        QtMocHelpers::MethodData<QString(double) const>(51, 4, QMC::AccessPublic, QMetaType::QString, {{
            { QMetaType::Double, 27 },
        }}),
        // Method 'loadSetupSpecs'
        QtMocHelpers::MethodData<void()>(52, 4, QMC::AccessPublic, QMetaType::Void),
        // Method 'connectProvider'
        QtMocHelpers::MethodData<void(const QString &, const QVariantMap &, bool)>(53, 4, QMC::AccessPublic, QMetaType::Void, {{
            { QMetaType::QString, 42 }, { 0x80000000 | 54, 55 }, { QMetaType::Bool, 56 },
        }}),
        // Method 'clearSetupResult'
        QtMocHelpers::MethodData<void()>(57, 4, QMC::AccessPublic, QMetaType::Void),
    };
    QtMocHelpers::UintData qt_properties {
        // property 'title'
        QtMocHelpers::PropertyData<QString>(58, QMetaType::QString, QMC::DefaultPropertyFlags, 0),
        // property 'artist'
        QtMocHelpers::PropertyData<QString>(59, QMetaType::QString, QMC::DefaultPropertyFlags, 0),
        // property 'album'
        QtMocHelpers::PropertyData<QString>(60, QMetaType::QString, QMC::DefaultPropertyFlags, 0),
        // property 'albumArtUrl'
        QtMocHelpers::PropertyData<QString>(61, QMetaType::QString, QMC::DefaultPropertyFlags, 0),
        // property 'state'
        QtMocHelpers::PropertyData<QString>(62, QMetaType::QString, QMC::DefaultPropertyFlags, 1),
        // property 'format'
        QtMocHelpers::PropertyData<QString>(63, QMetaType::QString, QMC::DefaultPropertyFlags, 0),
        // property 'sourceLabel'
        QtMocHelpers::PropertyData<QString>(64, QMetaType::QString, QMC::DefaultPropertyFlags, 0),
        // property 'audioDevice'
        QtMocHelpers::PropertyData<QString>(65, QMetaType::QString, QMC::DefaultPropertyFlags, 1),
        // property 'seekable'
        QtMocHelpers::PropertyData<bool>(66, QMetaType::Bool, QMC::DefaultPropertyFlags, 1),
        // property 'position'
        QtMocHelpers::PropertyData<double>(67, QMetaType::Double, QMC::DefaultPropertyFlags, 2),
        // property 'duration'
        QtMocHelpers::PropertyData<double>(68, QMetaType::Double, QMC::DefaultPropertyFlags, 2),
        // property 'volume'
        QtMocHelpers::PropertyData<double>(69, QMetaType::Double, QMC::DefaultPropertyFlags, 3),
        // property 'shuffle'
        QtMocHelpers::PropertyData<bool>(70, QMetaType::Bool, QMC::DefaultPropertyFlags, 4),
        // property 'repeat'
        QtMocHelpers::PropertyData<QString>(71, QMetaType::QString, QMC::DefaultPropertyFlags, 4),
        // property 'mono'
        QtMocHelpers::PropertyData<bool>(72, QMetaType::Bool, QMC::DefaultPropertyFlags, 4),
        // property 'speed'
        QtMocHelpers::PropertyData<double>(73, QMetaType::Double, QMC::DefaultPropertyFlags, 4),
        // property 'eqPreset'
        QtMocHelpers::PropertyData<QString>(74, QMetaType::QString, QMC::DefaultPropertyFlags, 5),
        // property 'eqBands'
        QtMocHelpers::PropertyData<QVariantList>(75, 0x80000000 | 19, QMC::DefaultPropertyFlags | QMC::EnumOrFlag, 5),
        // property 'eqPresets'
        QtMocHelpers::PropertyData<QStringList>(76, QMetaType::QStringList, QMC::DefaultPropertyFlags, 6),
        // property 'currentIndex'
        QtMocHelpers::PropertyData<int>(77, QMetaType::Int, QMC::DefaultPropertyFlags, 7),
        // property 'queueCount'
        QtMocHelpers::PropertyData<int>(78, QMetaType::Int, QMC::DefaultPropertyFlags, 7),
        // property 'connected'
        QtMocHelpers::PropertyData<bool>(79, QMetaType::Bool, QMC::DefaultPropertyFlags, 8),
        // property 'connectionMessage'
        QtMocHelpers::PropertyData<QString>(80, QMetaType::QString, QMC::DefaultPropertyFlags, 8),
        // property 'selectedProvider'
        QtMocHelpers::PropertyData<QString>(81, QMetaType::QString, QMC::DefaultPropertyFlags, 9),
        // property 'selectedProviderSearchable'
        QtMocHelpers::PropertyData<bool>(82, QMetaType::Bool, QMC::DefaultPropertyFlags, 9),
        // property 'selectedPlaylist'
        QtMocHelpers::PropertyData<QString>(83, QMetaType::QString, QMC::DefaultPropertyFlags, 10),
        // property 'desktopTheme'
        QtMocHelpers::PropertyData<QVariantMap>(84, 0x80000000 | 54, QMC::DefaultPropertyFlags | QMC::EnumOrFlag, 11),
        // property 'omarchySession'
        QtMocHelpers::PropertyData<bool>(85, QMetaType::Bool, QMC::DefaultPropertyFlags | QMC::Constant),
        // property 'setupSpecs'
        QtMocHelpers::PropertyData<QVariantList>(86, 0x80000000 | 19, QMC::DefaultPropertyFlags | QMC::EnumOrFlag, 12),
        // property 'setupBusy'
        QtMocHelpers::PropertyData<bool>(87, QMetaType::Bool, QMC::DefaultPropertyFlags, 13),
        // property 'setupMessage'
        QtMocHelpers::PropertyData<QString>(88, QMetaType::QString, QMC::DefaultPropertyFlags, 13),
        // property 'setupFailed'
        QtMocHelpers::PropertyData<bool>(89, QMetaType::Bool, QMC::DefaultPropertyFlags, 13),
        // property 'setupCanForce'
        QtMocHelpers::PropertyData<bool>(90, QMetaType::Bool, QMC::DefaultPropertyFlags, 13),
        // property 'queueModel'
        QtMocHelpers::PropertyData<QAbstractItemModel*>(91, 0x80000000 | 92, QMC::DefaultPropertyFlags | QMC::EnumOrFlag | QMC::Constant),
        // property 'providersModel'
        QtMocHelpers::PropertyData<QAbstractItemModel*>(93, 0x80000000 | 92, QMC::DefaultPropertyFlags | QMC::EnumOrFlag | QMC::Constant),
        // property 'providerPlaylistsModel'
        QtMocHelpers::PropertyData<QAbstractItemModel*>(94, 0x80000000 | 92, QMC::DefaultPropertyFlags | QMC::EnumOrFlag | QMC::Constant),
        // property 'libraryTracksModel'
        QtMocHelpers::PropertyData<QAbstractItemModel*>(95, 0x80000000 | 92, QMC::DefaultPropertyFlags | QMC::EnumOrFlag | QMC::Constant),
        // property 'radioModel'
        QtMocHelpers::PropertyData<QAbstractItemModel*>(96, 0x80000000 | 92, QMC::DefaultPropertyFlags | QMC::EnumOrFlag | QMC::Constant),
    };
    QtMocHelpers::UintData qt_enums {
    };
    QtMocHelpers::UintData qt_constructors {};
    QtMocHelpers::ClassInfos qt_classinfo({
            {    1,    2 },
    });
    return QtMocHelpers::metaObjectData<GuiController, void>(QMC::MetaObjectFlag{}, qt_stringData,
            qt_methods, qt_properties, qt_enums, qt_constructors, qt_classinfo);
}
Q_CONSTINIT const QMetaObject GuiController::staticMetaObject = { {
    QMetaObject::SuperData::link<QObject::staticMetaObject>(),
    qt_staticMetaObjectStaticContent<qt_meta_tag_ZN13GuiControllerE_t>.stringdata,
    qt_staticMetaObjectStaticContent<qt_meta_tag_ZN13GuiControllerE_t>.data,
    qt_static_metacall,
    nullptr,
    qt_staticMetaObjectRelocatingContent<qt_meta_tag_ZN13GuiControllerE_t>.metaTypes,
    nullptr
} };

void GuiController::qt_static_metacall(QObject *_o, QMetaObject::Call _c, int _id, void **_a)
{
    auto *_t = static_cast<GuiController *>(_o);
    if (_c == QMetaObject::InvokeMetaMethod) {
        switch (_id) {
        case 0: _t->trackChanged(); break;
        case 1: _t->stateChanged(); break;
        case 2: _t->positionChanged(); break;
        case 3: _t->volumeChanged(); break;
        case 4: _t->playbackModesChanged(); break;
        case 5: _t->equalizerChanged(); break;
        case 6: _t->eqPresetsChanged(); break;
        case 7: _t->queueChanged(); break;
        case 8: _t->connectionChanged(); break;
        case 9: _t->selectedProviderChanged(); break;
        case 10: _t->selectedPlaylistChanged(); break;
        case 11: _t->desktopThemeChanged(); break;
        case 12: _t->setupSpecsChanged(); break;
        case 13: _t->setupStateChanged(); break;
        case 14: _t->bandsChanged((*reinterpret_cast<std::add_pointer_t<QVariantList>>(_a[1]))); break;
        case 15: _t->startDaemon(); break;
        case 16: _t->toggle(); break;
        case 17: _t->stop(); break;
        case 18: _t->next(); break;
        case 19: _t->previous(); break;
        case 20: _t->seekTo((*reinterpret_cast<std::add_pointer_t<double>>(_a[1]))); break;
        case 21: _t->setVolume((*reinterpret_cast<std::add_pointer_t<double>>(_a[1]))); break;
        case 22: _t->setShuffle((*reinterpret_cast<std::add_pointer_t<bool>>(_a[1]))); break;
        case 23: _t->cycleRepeat(); break;
        case 24: _t->setEqBand((*reinterpret_cast<std::add_pointer_t<int>>(_a[1])),(*reinterpret_cast<std::add_pointer_t<double>>(_a[2]))); break;
        case 25: _t->setEqPreset((*reinterpret_cast<std::add_pointer_t<QString>>(_a[1]))); break;
        case 26: _t->playQueue((*reinterpret_cast<std::add_pointer_t<int>>(_a[1]))); break;
        case 27: _t->clearQueue(); break;
        case 28: _t->selectProvider((*reinterpret_cast<std::add_pointer_t<QString>>(_a[1]))); break;
        case 29: _t->browseProviderPlaylist((*reinterpret_cast<std::add_pointer_t<QString>>(_a[1]))); break;
        case 30: _t->loadSelectedPlaylist(); break;
        case 31: _t->playLibraryTrack((*reinterpret_cast<std::add_pointer_t<int>>(_a[1]))); break;
        case 32: _t->searchProvider((*reinterpret_cast<std::add_pointer_t<QString>>(_a[1]))); break;
        case 33: _t->loadRadioStation((*reinterpret_cast<std::add_pointer_t<QString>>(_a[1]))); break;
        case 34: { QString _r = _t->formatDuration((*reinterpret_cast<std::add_pointer_t<double>>(_a[1])));
            if (_a[0]) *reinterpret_cast<QString*>(_a[0]) = std::move(_r); }  break;
        case 35: _t->loadSetupSpecs(); break;
        case 36: _t->connectProvider((*reinterpret_cast<std::add_pointer_t<QString>>(_a[1])),(*reinterpret_cast<std::add_pointer_t<QVariantMap>>(_a[2])),(*reinterpret_cast<std::add_pointer_t<bool>>(_a[3]))); break;
        case 37: _t->clearSetupResult(); break;
        default: ;
        }
    }
    if (_c == QMetaObject::IndexOfMethod) {
        if (QtMocHelpers::indexOfMethod<void (GuiController::*)()>(_a, &GuiController::trackChanged, 0))
            return;
        if (QtMocHelpers::indexOfMethod<void (GuiController::*)()>(_a, &GuiController::stateChanged, 1))
            return;
        if (QtMocHelpers::indexOfMethod<void (GuiController::*)()>(_a, &GuiController::positionChanged, 2))
            return;
        if (QtMocHelpers::indexOfMethod<void (GuiController::*)()>(_a, &GuiController::volumeChanged, 3))
            return;
        if (QtMocHelpers::indexOfMethod<void (GuiController::*)()>(_a, &GuiController::playbackModesChanged, 4))
            return;
        if (QtMocHelpers::indexOfMethod<void (GuiController::*)()>(_a, &GuiController::equalizerChanged, 5))
            return;
        if (QtMocHelpers::indexOfMethod<void (GuiController::*)()>(_a, &GuiController::eqPresetsChanged, 6))
            return;
        if (QtMocHelpers::indexOfMethod<void (GuiController::*)()>(_a, &GuiController::queueChanged, 7))
            return;
        if (QtMocHelpers::indexOfMethod<void (GuiController::*)()>(_a, &GuiController::connectionChanged, 8))
            return;
        if (QtMocHelpers::indexOfMethod<void (GuiController::*)()>(_a, &GuiController::selectedProviderChanged, 9))
            return;
        if (QtMocHelpers::indexOfMethod<void (GuiController::*)()>(_a, &GuiController::selectedPlaylistChanged, 10))
            return;
        if (QtMocHelpers::indexOfMethod<void (GuiController::*)()>(_a, &GuiController::desktopThemeChanged, 11))
            return;
        if (QtMocHelpers::indexOfMethod<void (GuiController::*)()>(_a, &GuiController::setupSpecsChanged, 12))
            return;
        if (QtMocHelpers::indexOfMethod<void (GuiController::*)()>(_a, &GuiController::setupStateChanged, 13))
            return;
        if (QtMocHelpers::indexOfMethod<void (GuiController::*)(const QVariantList & )>(_a, &GuiController::bandsChanged, 14))
            return;
    }
    if (_c == QMetaObject::RegisterPropertyMetaType) {
        switch (_id) {
        default: *reinterpret_cast<int*>(_a[0]) = -1; break;
        case 37:
        case 36:
        case 35:
        case 34:
        case 33:
            *reinterpret_cast<int*>(_a[0]) = qRegisterMetaType< QAbstractItemModel* >(); break;
        }
    }
    if (_c == QMetaObject::ReadProperty) {
        void *_v = _a[0];
        switch (_id) {
        case 0: *reinterpret_cast<QString*>(_v) = _t->title(); break;
        case 1: *reinterpret_cast<QString*>(_v) = _t->artist(); break;
        case 2: *reinterpret_cast<QString*>(_v) = _t->album(); break;
        case 3: *reinterpret_cast<QString*>(_v) = _t->albumArtUrl(); break;
        case 4: *reinterpret_cast<QString*>(_v) = _t->state(); break;
        case 5: *reinterpret_cast<QString*>(_v) = _t->format(); break;
        case 6: *reinterpret_cast<QString*>(_v) = _t->sourceLabel(); break;
        case 7: *reinterpret_cast<QString*>(_v) = _t->audioDevice(); break;
        case 8: *reinterpret_cast<bool*>(_v) = _t->seekable(); break;
        case 9: *reinterpret_cast<double*>(_v) = _t->position(); break;
        case 10: *reinterpret_cast<double*>(_v) = _t->duration(); break;
        case 11: *reinterpret_cast<double*>(_v) = _t->volume(); break;
        case 12: *reinterpret_cast<bool*>(_v) = _t->shuffle(); break;
        case 13: *reinterpret_cast<QString*>(_v) = _t->repeat(); break;
        case 14: *reinterpret_cast<bool*>(_v) = _t->mono(); break;
        case 15: *reinterpret_cast<double*>(_v) = _t->speed(); break;
        case 16: *reinterpret_cast<QString*>(_v) = _t->eqPreset(); break;
        case 17: *reinterpret_cast<QVariantList*>(_v) = _t->eqBands(); break;
        case 18: *reinterpret_cast<QStringList*>(_v) = _t->eqPresets(); break;
        case 19: *reinterpret_cast<int*>(_v) = _t->currentIndex(); break;
        case 20: *reinterpret_cast<int*>(_v) = _t->queueCount(); break;
        case 21: *reinterpret_cast<bool*>(_v) = _t->connected(); break;
        case 22: *reinterpret_cast<QString*>(_v) = _t->connectionMessage(); break;
        case 23: *reinterpret_cast<QString*>(_v) = _t->selectedProvider(); break;
        case 24: *reinterpret_cast<bool*>(_v) = _t->selectedProviderSearchable(); break;
        case 25: *reinterpret_cast<QString*>(_v) = _t->selectedPlaylist(); break;
        case 26: *reinterpret_cast<QVariantMap*>(_v) = _t->desktopTheme(); break;
        case 27: *reinterpret_cast<bool*>(_v) = _t->omarchySession(); break;
        case 28: *reinterpret_cast<QVariantList*>(_v) = _t->setupSpecs(); break;
        case 29: *reinterpret_cast<bool*>(_v) = _t->setupBusy(); break;
        case 30: *reinterpret_cast<QString*>(_v) = _t->setupMessage(); break;
        case 31: *reinterpret_cast<bool*>(_v) = _t->setupFailed(); break;
        case 32: *reinterpret_cast<bool*>(_v) = _t->setupCanForce(); break;
        case 33: *reinterpret_cast<QAbstractItemModel**>(_v) = _t->queueModel(); break;
        case 34: *reinterpret_cast<QAbstractItemModel**>(_v) = _t->providersModel(); break;
        case 35: *reinterpret_cast<QAbstractItemModel**>(_v) = _t->providerPlaylistsModel(); break;
        case 36: *reinterpret_cast<QAbstractItemModel**>(_v) = _t->libraryTracksModel(); break;
        case 37: *reinterpret_cast<QAbstractItemModel**>(_v) = _t->radioModel(); break;
        default: break;
        }
    }
}

const QMetaObject *GuiController::metaObject() const
{
    return QObject::d_ptr->metaObject ? QObject::d_ptr->dynamicMetaObject() : &staticMetaObject;
}

void *GuiController::qt_metacast(const char *_clname)
{
    if (!_clname) return nullptr;
    if (!strcmp(_clname, qt_staticMetaObjectStaticContent<qt_meta_tag_ZN13GuiControllerE_t>.strings))
        return static_cast<void*>(this);
    return QObject::qt_metacast(_clname);
}

int GuiController::qt_metacall(QMetaObject::Call _c, int _id, void **_a)
{
    _id = QObject::qt_metacall(_c, _id, _a);
    if (_id < 0)
        return _id;
    if (_c == QMetaObject::InvokeMetaMethod) {
        if (_id < 38)
            qt_static_metacall(this, _c, _id, _a);
        _id -= 38;
    }
    if (_c == QMetaObject::RegisterMethodArgumentMetaType) {
        if (_id < 38)
            *reinterpret_cast<QMetaType *>(_a[0]) = QMetaType();
        _id -= 38;
    }
    if (_c == QMetaObject::ReadProperty || _c == QMetaObject::WriteProperty
            || _c == QMetaObject::ResetProperty || _c == QMetaObject::BindableProperty
            || _c == QMetaObject::RegisterPropertyMetaType) {
        qt_static_metacall(this, _c, _id, _a);
        _id -= 38;
    }
    return _id;
}

// SIGNAL 0
void GuiController::trackChanged()
{
    QMetaObject::activate(this, &staticMetaObject, 0, nullptr);
}

// SIGNAL 1
void GuiController::stateChanged()
{
    QMetaObject::activate(this, &staticMetaObject, 1, nullptr);
}

// SIGNAL 2
void GuiController::positionChanged()
{
    QMetaObject::activate(this, &staticMetaObject, 2, nullptr);
}

// SIGNAL 3
void GuiController::volumeChanged()
{
    QMetaObject::activate(this, &staticMetaObject, 3, nullptr);
}

// SIGNAL 4
void GuiController::playbackModesChanged()
{
    QMetaObject::activate(this, &staticMetaObject, 4, nullptr);
}

// SIGNAL 5
void GuiController::equalizerChanged()
{
    QMetaObject::activate(this, &staticMetaObject, 5, nullptr);
}

// SIGNAL 6
void GuiController::eqPresetsChanged()
{
    QMetaObject::activate(this, &staticMetaObject, 6, nullptr);
}

// SIGNAL 7
void GuiController::queueChanged()
{
    QMetaObject::activate(this, &staticMetaObject, 7, nullptr);
}

// SIGNAL 8
void GuiController::connectionChanged()
{
    QMetaObject::activate(this, &staticMetaObject, 8, nullptr);
}

// SIGNAL 9
void GuiController::selectedProviderChanged()
{
    QMetaObject::activate(this, &staticMetaObject, 9, nullptr);
}

// SIGNAL 10
void GuiController::selectedPlaylistChanged()
{
    QMetaObject::activate(this, &staticMetaObject, 10, nullptr);
}

// SIGNAL 11
void GuiController::desktopThemeChanged()
{
    QMetaObject::activate(this, &staticMetaObject, 11, nullptr);
}

// SIGNAL 12
void GuiController::setupSpecsChanged()
{
    QMetaObject::activate(this, &staticMetaObject, 12, nullptr);
}

// SIGNAL 13
void GuiController::setupStateChanged()
{
    QMetaObject::activate(this, &staticMetaObject, 13, nullptr);
}

// SIGNAL 14
void GuiController::bandsChanged(const QVariantList & _t1)
{
    QMetaObject::activate<void>(this, &staticMetaObject, 14, nullptr, _t1);
}
QT_WARNING_POP
