#include "guicontroller.h"

#include <QDir>
#include <QJsonArray>
#include <QJsonObject>
#include <QStandardPaths>
#include <QUrl>

#include <algorithm>
#include <cmath>

namespace {

QString defaultSocketPath()
{
    const QString configured = qEnvironmentVariable("CLIAMP_CONFIG_DIR");
    if (!configured.isEmpty()) {
        return QDir(configured).filePath("cliamp.sock");
    }

    const QString xdgConfig = qEnvironmentVariable("XDG_CONFIG_HOME");
    if (!xdgConfig.isEmpty()) {
        return QDir(xdgConfig).filePath("cliamp/cliamp.sock");
    }

    const QString home = qEnvironmentVariable("HOME");
    if (!home.isEmpty()) {
        return QDir(home).filePath(".config/cliamp/cliamp.sock");
    }

#ifdef Q_OS_WIN
    const QString appData = qEnvironmentVariable("APPDATA");
    if (!appData.isEmpty()) {
        return QDir(appData).filePath("cliamp/cliamp.sock");
    }
#endif

    return QDir(QDir::homePath()).filePath(".config/cliamp/cliamp.sock");
}

bool responseOK(const QJsonObject &response)
{
    return response.value("ok").toBool();
}

QString imageSource(const QString &source)
{
    if (source.isEmpty() || source.startsWith("http://") || source.startsWith("https://") ||
        source.startsWith("file://") || source.startsWith("qrc:/")) {
        return source;
    }
    return QUrl::fromLocalFile(source).toString();
}

} // namespace

GuiController::GuiController(QObject *parent)
    : QObject(parent)
    , socketPath_(defaultSocketPath())
    , ipc_(socketPath_, this)
    , queue_({"title", "artist", "album", "duration_secs", "path", "index", "queue_position", "bookmark", "unplayable"}, this)
    , providers_({"key", "name", "searchable", "browse_artists", "browse_albums", "catalog"}, this)
    , providerPlaylists_({"playlistId", "name", "provider", "section", "track_count", "duration_secs", "favoritable", "favorite"}, this)
    , libraryTracks_({"title", "artist", "album", "genre", "path", "album_art_url", "duration_secs", "index", "stream", "bookmark", "unplayable"}, this)
    , radio_({"playlistId", "name", "provider", "section", "track_count", "duration_secs", "favorite"}, this)
{
    eqBands_.fill(0.0, 10);

    statusTimer_.setInterval(750);
    queueTimer_.setInterval(1500);
    bandsTimer_.setInterval(33);
    positionTimer_.setInterval(250);

    connect(&statusTimer_, &QTimer::timeout, this, &GuiController::requestStatus);
    connect(&queueTimer_, &QTimer::timeout, this, &GuiController::requestQueue);
    connect(&bandsTimer_, &QTimer::timeout, this, &GuiController::requestBands);
    connect(&positionTimer_, &QTimer::timeout, this, [this] {
        if (state_ != "playing") {
            return;
        }
        position_ += 0.25 * speed_;
        if (duration_ > 0.0) {
            position_ = std::min(position_, duration_);
        }
        emit positionChanged();
    });
    connect(&daemonProcess_, &QProcess::errorOccurred, this, [this](QProcess::ProcessError) {
        if (ownsDaemon_) {
            setConnected(false, tr("Could not start cliamp: %1").arg(daemonProcess_.errorString()));
        }
    });
    connect(&daemonProcess_, &QProcess::finished, this, [this](int, QProcess::ExitStatus) {
        if (ownsDaemon_) {
            ownsDaemon_ = false;
            setConnected(false, tr("cliamp daemon stopped"));
        }
    });

    statusTimer_.start();
    queueTimer_.start();
    bandsTimer_.start();
    positionTimer_.start();
    requestStatus();
    requestQueue();
    loadProviders();
}

GuiController::~GuiController()
{
    if (ownsDaemon_ && daemonProcess_.state() != QProcess::NotRunning) {
        daemonProcess_.terminate();
        if (!daemonProcess_.waitForFinished(1500)) {
            daemonProcess_.kill();
        }
    }
}

QString GuiController::title() const { return title_; }
QString GuiController::artist() const { return artist_; }
QString GuiController::album() const { return album_; }
QString GuiController::albumArtUrl() const { return albumArtUrl_; }
QString GuiController::state() const { return state_; }
double GuiController::position() const { return position_; }
double GuiController::duration() const { return duration_; }
double GuiController::volume() const { return volume_; }
bool GuiController::shuffle() const { return shuffle_; }
QString GuiController::repeat() const { return repeat_; }
bool GuiController::mono() const { return mono_; }
double GuiController::speed() const { return speed_; }
QString GuiController::eqPreset() const { return eqPreset_; }
QVariantList GuiController::eqBands() const { return eqBands_; }
int GuiController::currentIndex() const { return currentIndex_; }
int GuiController::queueCount() const { return queue_.rowCount(); }
bool GuiController::connected() const { return connected_; }
QString GuiController::connectionMessage() const { return connectionMessage_; }
QString GuiController::selectedProvider() const { return selectedProvider_; }
QString GuiController::selectedPlaylist() const { return selectedPlaylist_; }

QAbstractItemModel *GuiController::queueModel() { return &queue_; }
QAbstractItemModel *GuiController::providersModel() { return &providers_; }
QAbstractItemModel *GuiController::providerPlaylistsModel() { return &providerPlaylists_; }
QAbstractItemModel *GuiController::libraryTracksModel() { return &libraryTracks_; }
QAbstractItemModel *GuiController::radioModel() { return &radio_; }

void GuiController::startDaemon()
{
    if (daemonProcess_.state() != QProcess::NotRunning) {
        return;
    }

    QString executable = qEnvironmentVariable("CLIAMP_BIN");
    if (executable.isEmpty()) {
        executable = QStandardPaths::findExecutable("cliamp");
    }
    if (executable.isEmpty()) {
        setConnected(false, tr("cliamp was not found. Set CLIAMP_BIN or add it to PATH."));
        return;
    }

    ownsDaemon_ = true;
    setConnected(false, tr("Starting cliamp daemon..."));
    daemonProcess_.setProgram(executable);
    daemonProcess_.setArguments({"--daemon"});
    daemonProcess_.start();
}

void GuiController::toggle() { runCommand("toggle"); }
void GuiController::stop() { runCommand("stop"); }
void GuiController::next() { runCommand("next"); }
void GuiController::previous() { runCommand("prev"); }

void GuiController::seekTo(double seconds)
{
    if (duration_ > 0.0) {
        seconds = std::clamp(seconds, 0.0, duration_);
    } else {
        seconds = std::max(0.0, seconds);
    }
    if (std::abs(seconds - position_) < 0.01) {
        return;
    }
    request({{"cmd", "seek.set"}, {"value", seconds}}, 3000, [this, seconds](const QJsonObject &response) {
        if (responseOK(response)) {
            position_ = seconds;
            emit positionChanged();
        }
        afterPlaybackCommand();
    });
}

void GuiController::setVolume(double db)
{
    volume_ = std::clamp(db, -90.0, 6.0);
    pendingVolume_ = volume_;
    emit volumeChanged();
    flushVolume();
}

void GuiController::setShuffle(bool enabled)
{
    request({{"cmd", "shuffle"}, {"name", enabled ? "on" : "off"}}, 3000, [this](const QJsonObject &response) {
        if (responseOK(response)) {
            shuffle_ = response.value("shuffle").toBool();
            emit playbackModesChanged();
        }
    });
}

void GuiController::cycleRepeat()
{
    request({{"cmd", "repeat"}, {"name", "cycle"}}, 3000, [this](const QJsonObject &response) {
        if (responseOK(response)) {
            repeat_ = response.value("repeat").toString(repeat_);
            emit playbackModesChanged();
        }
    });
}

void GuiController::setEqBand(int band, double gain)
{
    if (band < 0 || band >= eqBands_.size()) {
        return;
    }
    gain = std::clamp(gain, -12.0, 12.0);
    eqBands_[band] = gain;
    eqPreset_ = QStringLiteral("Custom");
    pendingEqBands_[band] = gain;
    emit equalizerChanged();
    flushEqualizer();
}

void GuiController::setEqPreset(const QString &preset)
{
    pendingEqPreset_ = preset;
    pendingEqBands_.fill(std::nullopt);
    flushEqualizer();
}

void GuiController::playQueue(int index)
{
    request({{"cmd", "queue.play"}, {"index", index}}, 5000, [this](const QJsonObject &response) {
        if (responseOK(response)) {
            queue_.setItems(response.value("tracks").toArray());
            currentIndex_ = response.value("index").toInt(-1);
            emit queueChanged();
        }
        afterPlaybackCommand();
    });
}

void GuiController::clearQueue()
{
    request({{"cmd", "queue.clear"}}, 5000, [this](const QJsonObject &response) {
        if (responseOK(response)) {
            queue_.setItems(response.value("tracks").toArray());
            currentIndex_ = -1;
            emit queueChanged();
        }
        afterPlaybackCommand();
    });
}

void GuiController::selectProvider(const QString &key)
{
    if (key.isEmpty()) {
        return;
    }
    selectedProvider_ = key;
    selectedPlaylist_.clear();
    providerPlaylists_.clear();
    libraryTracks_.clear();
    emit selectedProviderChanged();
    emit selectedPlaylistChanged();

    const quint64 requestGeneration = ++libraryRequestGeneration_;
    request({{"cmd", "provider.playlists"}, {"provider", key}}, 30000,
            [this, key, requestGeneration](const QJsonObject &response) {
        if (!responseOK(response) || requestGeneration != libraryRequestGeneration_ || selectedProvider_ != key) {
            return;
        }
        providerPlaylists_.setItems(response.value("playlists").toArray());
        if (key == "radio") {
            radio_.setItems(response.value("playlists").toArray());
        }
    });
}

void GuiController::browseProviderPlaylist(const QString &playlist)
{
    if (selectedProvider_.isEmpty() || playlist.isEmpty()) {
        return;
    }
    selectedPlaylist_ = playlist;
    emit selectedPlaylistChanged();
    const QString provider = selectedProvider_;
    const quint64 requestGeneration = ++libraryRequestGeneration_;
    request({{"cmd", "provider.tracks"}, {"provider", provider}, {"playlist", playlist}}, 30000,
            [this, provider, playlist, requestGeneration](const QJsonObject &response) {
                if (responseOK(response) && requestGeneration == libraryRequestGeneration_ &&
                    selectedProvider_ == provider && selectedPlaylist_ == playlist) {
                    libraryTracks_.setItems(response.value("tracks").toArray());
                }
            });
}

void GuiController::loadSelectedPlaylist()
{
    if (selectedProvider_.isEmpty() || selectedPlaylist_.isEmpty()) {
        return;
    }
    request({{"cmd", "provider.load"}, {"provider", selectedProvider_}, {"playlist", selectedPlaylist_}}, 30000,
            [this](const QJsonObject &response) {
                if (responseOK(response)) {
                    afterPlaybackCommand();
                }
            });
}

void GuiController::playLibraryTrack(int index)
{
    const QVariantMap item = libraryTracks_.get(index);
    if (item.isEmpty()) {
        return;
    }
    request({{"cmd", "track.play"}, {"track", QJsonObject::fromVariantMap(item)}}, 10000,
            [this](const QJsonObject &response) {
                if (responseOK(response)) {
                    afterPlaybackCommand();
                }
            });
}

void GuiController::searchProvider(const QString &query)
{
    if (selectedProvider_.isEmpty() || query.trimmed().isEmpty()) {
        return;
    }
    const QString provider = selectedProvider_;
    const quint64 requestGeneration = ++libraryRequestGeneration_;
    request({{"cmd", "provider.search"}, {"provider", provider}, {"query", query}, {"limit", 50}}, 30000,
            [this, provider, requestGeneration](const QJsonObject &response) {
                if (responseOK(response) && requestGeneration == libraryRequestGeneration_ && selectedProvider_ == provider) {
                    libraryTracks_.setItems(response.value("tracks").toArray());
                }
            });
}

void GuiController::loadRadioStation(const QString &station)
{
    if (station.isEmpty()) {
        return;
    }
    if (selectedProvider_ != "radio") {
        selectedProvider_ = "radio";
        emit selectedProviderChanged();
    }
    selectedPlaylist_ = station;
    emit selectedPlaylistChanged();
    loadSelectedPlaylist();
}

QString GuiController::formatDuration(double seconds) const
{
    if (!std::isfinite(seconds) || seconds < 0.0) {
        return QStringLiteral("--:--");
    }
    const qint64 total = static_cast<qint64>(std::floor(seconds));
    const qint64 hours = total / 3600;
    const qint64 minutes = (total % 3600) / 60;
    const qint64 remaining = total % 60;
    if (hours > 0) {
        return QStringLiteral("%1:%2:%3").arg(hours).arg(minutes, 2, 10, QLatin1Char('0')).arg(remaining, 2, 10, QLatin1Char('0'));
    }
    return QStringLiteral("%1:%2").arg(minutes).arg(remaining, 2, 10, QLatin1Char('0'));
}

void GuiController::request(const QJsonObject &request, int timeoutMs, ResponseHandler handler)
{
    ipc_.request(request, timeoutMs, [handler = std::move(handler)](const QJsonObject &response) {
        if (handler) {
            handler(response);
        }
    });
}

void GuiController::requestStatus()
{
    if (statusPending_) {
        return;
    }
    statusPending_ = true;
    request({{"cmd", "status"}}, 1000, [this](const QJsonObject &response) {
        statusPending_ = false;
        if (!responseOK(response)) {
            setConnected(false, response.value("error").toString(tr("cliamp daemon is not running")));
            return;
        }
        const bool wasConnected = connected_;
        setConnected(true);
        applyStatus(response);
        if (providers_.rowCount() == 0) {
            loadProviders();
        }
        if (!wasConnected && radio_.rowCount() == 0) {
            loadRadio();
        }
    });
}

void GuiController::requestQueue()
{
    if (queuePending_) {
        return;
    }
    queuePending_ = true;
    request({{"cmd", "queue.list"}}, 3000, [this](const QJsonObject &response) {
        queuePending_ = false;
        if (!responseOK(response)) {
            return;
        }
        queue_.setItems(response.value("tracks").toArray());
        currentIndex_ = response.value("index").toInt(-1);
        emit queueChanged();
    });
}

void GuiController::requestBands()
{
    if (!connected_ || bandsPending_) {
        return;
    }
    bandsPending_ = true;
    request({{"cmd", "bands"}}, 1000, [this](const QJsonObject &response) {
        bandsPending_ = false;
        if (!responseOK(response)) {
            return;
        }
        QVariantList bands;
        for (const QJsonValue &value : response.value("bands").toArray()) {
            bands.append(value.toDouble());
        }
        emit bandsChanged(bands);
    });
}

void GuiController::loadProviders()
{
    request({{"cmd", "provider.list"}}, 10000, [this](const QJsonObject &response) {
        if (!responseOK(response)) {
            return;
        }
        providers_.setItems(response.value("providers").toArray());
        if (selectedProvider_.isEmpty() && providers_.rowCount() > 0) {
            selectProvider(providers_.get(0).value("key").toString());
        }
    });
}

void GuiController::loadRadio()
{
    request({{"cmd", "provider.playlists"}, {"provider", "radio"}}, 30000, [this](const QJsonObject &response) {
        if (responseOK(response)) {
            radio_.setItems(response.value("playlists").toArray());
        }
    });
}

void GuiController::applyStatus(const QJsonObject &response)
{
    const QJsonObject track = response.value("track").toObject();
    title_ = track.value("title").toString(tr("No track loaded"));
    artist_ = track.value("artist").toString();
    album_ = track.value("album").toString();
    albumArtUrl_ = imageSource(track.value("album_art_url").toString());
    emit trackChanged();

    state_ = response.value("state").toString("stopped");
    emit stateChanged();

    position_ = response.value("position").toDouble();
    duration_ = response.value("duration").toDouble();
    emit positionChanged();

    if (!volumeRequestPending_ && !pendingVolume_) {
        volume_ = response.value("volume").toDouble(volume_);
        emit volumeChanged();
    }

    shuffle_ = response.value("shuffle").toBool();
    repeat_ = response.value("repeat").toString("off");
    mono_ = response.value("mono").toBool();
    speed_ = response.value("speed").toDouble(1.0);
    emit playbackModesChanged();

    if (!equalizerPending()) {
        eqPreset_ = response.value("eq_preset").toString("Custom");
        QVariantList bands;
        for (const QJsonValue &value : response.value("eq_bands").toArray()) {
            bands.append(value.toDouble());
        }
        if (bands.size() == 10) {
            eqBands_ = bands;
        }
        emit equalizerChanged();
    }
}

void GuiController::setConnected(bool connected, const QString &message)
{
    const QString nextMessage = connected ? QString() : message;
    if (connected_ == connected && connectionMessage_ == nextMessage) {
        return;
    }
    connected_ = connected;
    connectionMessage_ = nextMessage;
    emit connectionChanged();
}

void GuiController::runCommand(const QString &command)
{
    request({{"cmd", command}}, 3000, [this](const QJsonObject &) { afterPlaybackCommand(); });
}

void GuiController::afterPlaybackCommand()
{
    requestStatus();
    requestQueue();
}

void GuiController::flushVolume()
{
    if (volumeRequestPending_ || !pendingVolume_) {
        return;
    }

    const double volume = *pendingVolume_;
    pendingVolume_.reset();
    volumeRequestPending_ = true;
    request({{"cmd", "volume"}, {"value", volume}}, 3000, [this](const QJsonObject &response) {
        volumeRequestPending_ = false;
        if (!responseOK(response) && !pendingVolume_) {
            requestStatus();
        }
        flushVolume();
    });
}

void GuiController::flushEqualizer()
{
    if (equalizerRequestPending_) {
        return;
    }

    if (pendingEqPreset_) {
        const QString preset = *pendingEqPreset_;
        pendingEqPreset_.reset();
        equalizerRequestPending_ = true;
        request({{"cmd", "eq"}, {"name", preset}}, 3000, [this, preset](const QJsonObject &response) {
            equalizerRequestPending_ = false;
            if (responseOK(response)) {
                eqPreset_ = response.value("eq_preset").toString(preset);
                emit equalizerChanged();
            } else if (!equalizerPending()) {
                requestStatus();
            }
            flushEqualizer();
        });
        return;
    }

    for (int band = 0; band < static_cast<int>(pendingEqBands_.size()); ++band) {
        if (!pendingEqBands_[band]) {
            continue;
        }
        const double gain = *pendingEqBands_[band];
        pendingEqBands_[band].reset();
        equalizerRequestPending_ = true;
        request({{"cmd", "eq"}, {"band", band}, {"value", gain}}, 3000,
                [this](const QJsonObject &response) {
                    equalizerRequestPending_ = false;
                    if (!responseOK(response) && !equalizerPending()) {
                        requestStatus();
                    }
                    flushEqualizer();
                });
        return;
    }
}

bool GuiController::equalizerPending() const
{
    if (equalizerRequestPending_ || pendingEqPreset_) {
        return true;
    }
    return std::any_of(pendingEqBands_.cbegin(), pendingEqBands_.cend(), [](const auto &gain) {
        return gain.has_value();
    });
}
