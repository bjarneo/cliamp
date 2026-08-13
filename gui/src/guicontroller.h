#pragma once

#include "ipcclient.h"
#include "listmodel.h"

#include <QProcess>
#include <QTimer>
#include <QtQmlIntegration/qqmlintegration.h>

#include <array>
#include <optional>

class GuiController : public QObject
{
    Q_OBJECT
    QML_ELEMENT
    Q_PROPERTY(QString title READ title NOTIFY trackChanged)
    Q_PROPERTY(QString artist READ artist NOTIFY trackChanged)
    Q_PROPERTY(QString album READ album NOTIFY trackChanged)
    Q_PROPERTY(QString albumArtUrl READ albumArtUrl NOTIFY trackChanged)
    Q_PROPERTY(QString state READ state NOTIFY stateChanged)
    Q_PROPERTY(double position READ position NOTIFY positionChanged)
    Q_PROPERTY(double duration READ duration NOTIFY positionChanged)
    Q_PROPERTY(double volume READ volume NOTIFY volumeChanged)
    Q_PROPERTY(bool shuffle READ shuffle NOTIFY playbackModesChanged)
    Q_PROPERTY(QString repeat READ repeat NOTIFY playbackModesChanged)
    Q_PROPERTY(bool mono READ mono NOTIFY playbackModesChanged)
    Q_PROPERTY(double speed READ speed NOTIFY playbackModesChanged)
    Q_PROPERTY(QString eqPreset READ eqPreset NOTIFY equalizerChanged)
    Q_PROPERTY(QVariantList eqBands READ eqBands NOTIFY equalizerChanged)
    Q_PROPERTY(int currentIndex READ currentIndex NOTIFY queueChanged)
    Q_PROPERTY(int queueCount READ queueCount NOTIFY queueChanged)
    Q_PROPERTY(bool connected READ connected NOTIFY connectionChanged)
    Q_PROPERTY(QString connectionMessage READ connectionMessage NOTIFY connectionChanged)
    Q_PROPERTY(QString selectedProvider READ selectedProvider NOTIFY selectedProviderChanged)
    Q_PROPERTY(QString selectedPlaylist READ selectedPlaylist NOTIFY selectedPlaylistChanged)
    Q_PROPERTY(QAbstractItemModel *queueModel READ queueModel CONSTANT)
    Q_PROPERTY(QAbstractItemModel *providersModel READ providersModel CONSTANT)
    Q_PROPERTY(QAbstractItemModel *providerPlaylistsModel READ providerPlaylistsModel CONSTANT)
    Q_PROPERTY(QAbstractItemModel *libraryTracksModel READ libraryTracksModel CONSTANT)
    Q_PROPERTY(QAbstractItemModel *radioModel READ radioModel CONSTANT)

public:
    explicit GuiController(QObject *parent = nullptr);
    ~GuiController() override;

    [[nodiscard]] QString title() const;
    [[nodiscard]] QString artist() const;
    [[nodiscard]] QString album() const;
    [[nodiscard]] QString albumArtUrl() const;
    [[nodiscard]] QString state() const;
    [[nodiscard]] double position() const;
    [[nodiscard]] double duration() const;
    [[nodiscard]] double volume() const;
    [[nodiscard]] bool shuffle() const;
    [[nodiscard]] QString repeat() const;
    [[nodiscard]] bool mono() const;
    [[nodiscard]] double speed() const;
    [[nodiscard]] QString eqPreset() const;
    [[nodiscard]] QVariantList eqBands() const;
    [[nodiscard]] int currentIndex() const;
    [[nodiscard]] int queueCount() const;
    [[nodiscard]] bool connected() const;
    [[nodiscard]] QString connectionMessage() const;
    [[nodiscard]] QString selectedProvider() const;
    [[nodiscard]] QString selectedPlaylist() const;

    [[nodiscard]] QAbstractItemModel *queueModel();
    [[nodiscard]] QAbstractItemModel *providersModel();
    [[nodiscard]] QAbstractItemModel *providerPlaylistsModel();
    [[nodiscard]] QAbstractItemModel *libraryTracksModel();
    [[nodiscard]] QAbstractItemModel *radioModel();

    Q_INVOKABLE void startDaemon();
    Q_INVOKABLE void toggle();
    Q_INVOKABLE void stop();
    Q_INVOKABLE void next();
    Q_INVOKABLE void previous();
    Q_INVOKABLE void seekTo(double seconds);
    Q_INVOKABLE void setVolume(double db);
    Q_INVOKABLE void setShuffle(bool enabled);
    Q_INVOKABLE void cycleRepeat();
    Q_INVOKABLE void setEqBand(int band, double gain);
    Q_INVOKABLE void setEqPreset(const QString &preset);
    Q_INVOKABLE void playQueue(int index);
    Q_INVOKABLE void clearQueue();
    Q_INVOKABLE void selectProvider(const QString &key);
    Q_INVOKABLE void browseProviderPlaylist(const QString &playlist);
    Q_INVOKABLE void loadSelectedPlaylist();
    Q_INVOKABLE void playLibraryTrack(int index);
    Q_INVOKABLE void searchProvider(const QString &query);
    Q_INVOKABLE void loadRadioStation(const QString &station);
    Q_INVOKABLE QString formatDuration(double seconds) const;

signals:
    void trackChanged();
    void stateChanged();
    void positionChanged();
    void volumeChanged();
    void playbackModesChanged();
    void equalizerChanged();
    void queueChanged();
    void connectionChanged();
    void selectedProviderChanged();
    void selectedPlaylistChanged();
    void bandsChanged(const QVariantList &bands);

private:
    using ResponseHandler = std::function<void(const QJsonObject &)>;

    void request(const QJsonObject &request, int timeoutMs, ResponseHandler handler = {});
    void requestStatus();
    void requestQueue();
    void requestBands();
    void loadProviders();
    void loadRadio();
    void applyStatus(const QJsonObject &response);
    void setConnected(bool connected, const QString &message = {});
    void runCommand(const QString &command);
    void afterPlaybackCommand();
    void flushVolume();
    void flushEqualizer();
    [[nodiscard]] bool equalizerPending() const;

    QString socketPath_;
    IpcClient ipc_;
    QProcess daemonProcess_;
    QTimer statusTimer_;
    QTimer queueTimer_;
    QTimer bandsTimer_;
    QTimer positionTimer_;
    ListModel queue_;
    ListModel providers_;
    ListModel providerPlaylists_;
    ListModel libraryTracks_;
    ListModel radio_;

    QString title_ = tr("No track loaded");
    QString artist_;
    QString album_;
    QString albumArtUrl_;
    QString state_ = QStringLiteral("stopped");
    double position_ = 0.0;
    double duration_ = 0.0;
    double volume_ = -12.0;
    bool shuffle_ = false;
    QString repeat_ = QStringLiteral("off");
    bool mono_ = false;
    double speed_ = 1.0;
    QString eqPreset_ = QStringLiteral("Custom");
    QVariantList eqBands_;
    int currentIndex_ = -1;
    bool connected_ = false;
    QString connectionMessage_;
    QString selectedProvider_;
    QString selectedPlaylist_;
    quint64 libraryRequestGeneration_ = 0;
    bool statusPending_ = false;
    bool queuePending_ = false;
    bool bandsPending_ = false;
    bool volumeRequestPending_ = false;
    std::optional<double> pendingVolume_;
    bool equalizerRequestPending_ = false;
    std::optional<QString> pendingEqPreset_;
    std::array<std::optional<double>, 10> pendingEqBands_;
    bool ownsDaemon_ = false;
};
