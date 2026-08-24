//! Durable store-and-forward buffer for telemetry that cannot be published yet.
//!
//! A charger sits behind an LTE modem in a basement car park. The link drops for
//! minutes at a time, and the previous agent simply discarded whatever it could
//! not publish at that instant. Energy readings become invoices, so discarding
//! them is not an option.
//!
//! The spool is an append-only segmented log on local flash:
//!
//! ```text
//! spool/
//!   seg-00000000000000000007.log   <- oldest, being drained
//!   seg-00000000000000000008.log   <- newest, being appended
//!   cursor                          <- how far the drain has committed
//! ```
//!
//! Design constraints that shaped it:
//!
//! * **Power can be cut mid-write.** Every record carries a CRC and a length, so
//!   a torn tail is detectable. On start the newest segment is scanned and
//!   truncated back to the last intact record.
//! * **Flash is small and wears out.** Total size is capped. Segments are whole
//!   files so reclaiming space is an unlink, not a rewrite.
//! * **Redelivery is cheaper than loss.** The cursor is committed only after a
//!   successful publish, so a crash replays a few records. The cloud ingest is
//!   idempotent, so replay is harmless while loss is not.
//! * **No embedded database.** A segmented file keeps the agent a single static
//!   binary that cross-compiles to musl without C dependencies.

use std::collections::BTreeSet;
use std::fs::{self, File, OpenOptions};
use std::io::{self, BufReader, BufWriter, Read, Seek, SeekFrom, Write};
use std::path::{Path, PathBuf};

/// `u32` payload length followed by a `u32` CRC32 of the payload.
const RECORD_HEADER_BYTES: usize = 8;

/// Guards against acting on a length field that corruption turned into garbage.
const MAX_RECORD_BYTES: u32 = 1 << 20;

const SEGMENT_PREFIX: &str = "seg-";
const SEGMENT_SUFFIX: &str = ".log";
const CURSOR_FILE: &str = "cursor";

/// Position of the drain within the log.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Default)]
pub struct Cursor {
    pub segment: u64,
    pub offset: u64,
}

/// Sizing policy for the spool.
#[derive(Clone, Copy, Debug)]
pub struct SpoolConfig {
    /// Roll to a new segment once the active one reaches this size.
    pub segment_bytes: u64,
    /// Hard ceiling for the whole directory.
    pub max_total_bytes: u64,
}

impl Default for SpoolConfig {
    fn default() -> Self {
        // 4 MiB segments inside a 256 MiB budget: small enough to reclaim space
        // promptly on a constrained device, large enough that rotation is rare.
        Self {
            segment_bytes: 4 * 1024 * 1024,
            max_total_bytes: 256 * 1024 * 1024,
        }
    }
}

struct ActiveSegment {
    index: u64,
    file: BufWriter<File>,
    bytes: u64,
}

pub struct Spool {
    directory: PathBuf,
    config: SpoolConfig,
    segments: BTreeSet<u64>,
    active: Option<ActiveSegment>,
    cursor: Cursor,
    dropped_records: u64,
    corrupt_records: u64,
}

impl Spool {
    /// Opens (creating if needed) a spool directory, recovering any torn tail.
    pub fn open(directory: impl AsRef<Path>, config: SpoolConfig) -> io::Result<Self> {
        let directory = directory.as_ref().to_path_buf();
        fs::create_dir_all(&directory)?;

        let mut segments = BTreeSet::new();
        for entry in fs::read_dir(&directory)? {
            let entry = entry?;
            if let Some(index) = segment_index(&entry.file_name().to_string_lossy()) {
                segments.insert(index);
            }
        }

        let mut spool = Self {
            directory,
            config,
            segments,
            active: None,
            cursor: Cursor::default(),
            dropped_records: 0,
            corrupt_records: 0,
        };
        spool.cursor = spool.load_cursor()?;
        spool.recover_newest_segment()?;
        Ok(spool)
    }

    /// Appends one payload. Returns once the record is in the operating
    /// system's page cache; durability against power loss is bounded by the
    /// device's own flush behaviour, which is the same trade-off a database
    /// running without `fsync` per write would make.
    pub fn append(&mut self, payload: &[u8]) -> io::Result<()> {
        if payload.is_empty() {
            return Ok(());
        }
        if payload.len() > MAX_RECORD_BYTES as usize {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "record exceeds the maximum spool record size",
            ));
        }
        self.ensure_active_segment()?;

        let active = self
            .active
            .as_mut()
            .expect("ensure_active_segment guarantees an active segment");
        let checksum = crc32fast::hash(payload);
        active
            .file
            .write_all(&(payload.len() as u32).to_le_bytes())?;
        active.file.write_all(&checksum.to_le_bytes())?;
        active.file.write_all(payload)?;
        active.file.flush()?;
        active.bytes += (RECORD_HEADER_BYTES + payload.len()) as u64;

        self.enforce_capacity()?;
        Ok(())
    }

    /// Reads up to `max` records from the cursor without consuming them.
    /// The returned cursor is what [`Spool::commit`] should be given once the
    /// records have been published successfully.
    pub fn read_batch(&mut self, max: usize) -> io::Result<(Vec<Vec<u8>>, Cursor)> {
        let mut records = Vec::new();
        let mut cursor = self.cursor;
        if max == 0 {
            return Ok((records, cursor));
        }

        while let Some(segment) = self.segment_at_or_after(cursor.segment) {
            if segment != cursor.segment {
                cursor = Cursor { segment, offset: 0 };
            }

            let exhausted = self.read_from_segment(segment, &mut cursor, max, &mut records)?;
            // Stop once the caller has what it asked for, or once this segment
            // ended for a reason other than running out of records.
            if records.len() >= max || !exhausted {
                break;
            }
            match self.segment_after(segment) {
                Some(next) => {
                    cursor = Cursor {
                        segment: next,
                        offset: 0,
                    }
                }
                None => break,
            }
        }

        Ok((records, cursor))
    }

    /// Marks everything before `cursor` as delivered and reclaims whole
    /// segments that are now fully drained.
    pub fn commit(&mut self, cursor: Cursor) -> io::Result<()> {
        self.cursor = cursor;

        let reclaimable: Vec<u64> = self
            .segments
            .iter()
            .copied()
            .filter(|segment| *segment < cursor.segment)
            .collect();
        for segment in reclaimable {
            self.remove_segment(segment, false)?;
        }
        self.store_cursor()
    }

    /// Bytes still waiting to be published.
    pub fn pending_bytes(&self) -> u64 {
        self.segments
            .iter()
            .filter_map(|segment| fs::metadata(self.segment_path(*segment)).ok())
            .map(|metadata| metadata.len())
            .sum::<u64>()
            .saturating_sub(self.cursor.offset)
    }

    /// Records deleted because the spool hit its size ceiling.
    pub fn dropped_records(&self) -> u64 {
        self.dropped_records
    }

    /// Records abandoned because their checksum did not match.
    pub fn corrupt_records(&self) -> u64 {
        self.corrupt_records
    }

    pub fn is_empty(&mut self) -> io::Result<bool> {
        Ok(self.read_batch(1)?.0.is_empty())
    }

    fn read_from_segment(
        &mut self,
        segment: u64,
        cursor: &mut Cursor,
        max: usize,
        records: &mut Vec<Vec<u8>>,
    ) -> io::Result<bool> {
        let path = self.segment_path(segment);
        let file = match File::open(&path) {
            Ok(file) => file,
            Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(true),
            Err(error) => return Err(error),
        };
        let mut reader = BufReader::new(file);
        reader.seek(SeekFrom::Start(cursor.offset))?;

        while records.len() < max {
            match read_record(&mut reader)? {
                RecordRead::Record { payload, consumed } => {
                    records.push(payload);
                    cursor.offset += consumed;
                }
                RecordRead::EndOfSegment => return Ok(true),
                RecordRead::Corrupt => {
                    // A torn tail is expected after a power cut. Everything
                    // after it in this segment is unreadable, so move on rather
                    // than stalling the drain forever.
                    self.corrupt_records += 1;
                    return Ok(true);
                }
            }
        }
        Ok(false)
    }

    fn ensure_active_segment(&mut self) -> io::Result<()> {
        let needs_rotation = match &self.active {
            None => true,
            Some(active) => active.bytes >= self.config.segment_bytes,
        };
        if !needs_rotation {
            return Ok(());
        }

        let index = self.segments.iter().next_back().map_or(0, |last| last + 1);
        let file = OpenOptions::new()
            .create(true)
            .append(true)
            .open(self.segment_path(index))?;
        self.segments.insert(index);
        self.active = Some(ActiveSegment {
            index,
            file: BufWriter::new(file),
            bytes: 0,
        });
        Ok(())
    }

    /// Enforces the size ceiling by discarding the oldest segments.
    ///
    /// Discarding the oldest data is the lesser evil: the alternative is
    /// refusing new writes, which would make the charger stop recording
    /// entirely. The loss is counted so it can be alarmed on rather than
    /// happening silently.
    fn enforce_capacity(&mut self) -> io::Result<()> {
        while self.total_bytes()? > self.config.max_total_bytes {
            let Some(oldest) = self.segments.iter().next().copied() else {
                break;
            };
            if Some(oldest) == self.active.as_ref().map(|active| active.index) {
                // Never discard the segment currently being written; a single
                // oversized segment is preferable to losing the newest data.
                break;
            }
            self.remove_segment(oldest, true)?;
            if self.cursor.segment <= oldest {
                self.cursor = Cursor {
                    segment: self.segments.iter().next().copied().unwrap_or(oldest + 1),
                    offset: 0,
                };
                self.store_cursor()?;
            }
        }
        Ok(())
    }

    fn remove_segment(&mut self, segment: u64, count_as_dropped: bool) -> io::Result<()> {
        if count_as_dropped {
            self.dropped_records += self.count_records(segment).unwrap_or(0);
        }
        let path = self.segment_path(segment);
        match fs::remove_file(&path) {
            Ok(()) => {}
            Err(error) if error.kind() == io::ErrorKind::NotFound => {}
            Err(error) => return Err(error),
        }
        self.segments.remove(&segment);
        if Some(segment) == self.active.as_ref().map(|active| active.index) {
            self.active = None;
        }
        Ok(())
    }

    fn count_records(&self, segment: u64) -> io::Result<u64> {
        let file = File::open(self.segment_path(segment))?;
        let mut reader = BufReader::new(file);
        let mut count = 0;
        while let RecordRead::Record { .. } = read_record(&mut reader)? {
            count += 1;
        }
        Ok(count)
    }

    /// Scans the newest segment and truncates it back to the last intact
    /// record, so that appends after a power cut are not written behind
    /// unreadable bytes where the drain would never reach them.
    fn recover_newest_segment(&mut self) -> io::Result<()> {
        let Some(newest) = self.segments.iter().next_back().copied() else {
            return Ok(());
        };
        let path = self.segment_path(newest);
        let file = File::open(&path)?;
        let declared = file.metadata()?.len();
        let mut reader = BufReader::new(file);

        let mut valid = 0u64;
        while let RecordRead::Record { consumed, .. } = read_record(&mut reader)? {
            valid += consumed;
        }

        if valid < declared {
            let file = OpenOptions::new().write(true).open(&path)?;
            file.set_len(valid)?;
            self.corrupt_records += 1;
        }

        let handle = OpenOptions::new().create(true).append(true).open(&path)?;
        self.active = Some(ActiveSegment {
            index: newest,
            file: BufWriter::new(handle),
            bytes: valid,
        });
        Ok(())
    }

    fn total_bytes(&self) -> io::Result<u64> {
        let mut total = 0;
        for segment in &self.segments {
            if let Ok(metadata) = fs::metadata(self.segment_path(*segment)) {
                total += metadata.len();
            }
        }
        Ok(total)
    }

    fn segment_at_or_after(&self, segment: u64) -> Option<u64> {
        self.segments.range(segment..).next().copied()
    }

    fn segment_after(&self, segment: u64) -> Option<u64> {
        self.segments.range(segment + 1..).next().copied()
    }

    fn segment_path(&self, index: u64) -> PathBuf {
        self.directory
            .join(format!("{SEGMENT_PREFIX}{index:020}{SEGMENT_SUFFIX}"))
    }

    fn cursor_path(&self) -> PathBuf {
        self.directory.join(CURSOR_FILE)
    }

    fn load_cursor(&self) -> io::Result<Cursor> {
        let raw = match fs::read_to_string(self.cursor_path()) {
            Ok(raw) => raw,
            Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(Cursor::default()),
            Err(error) => return Err(error),
        };
        let mut parts = raw.split_whitespace();
        let cursor = match (parts.next(), parts.next()) {
            (Some(segment), Some(offset)) => Cursor {
                segment: segment.parse().unwrap_or(0),
                offset: offset.parse().unwrap_or(0),
            },
            _ => Cursor::default(),
        };
        // A cursor pointing at a reclaimed segment would stall the drain.
        if self.segments.contains(&cursor.segment) {
            Ok(cursor)
        } else {
            Ok(Cursor {
                segment: self.segments.iter().next().copied().unwrap_or(0),
                offset: 0,
            })
        }
    }

    /// Writes the cursor through a temporary file so an interrupted write
    /// cannot leave a half-written position behind.
    fn store_cursor(&self) -> io::Result<()> {
        let temporary = self.directory.join("cursor.tmp");
        {
            let mut file = File::create(&temporary)?;
            write!(file, "{} {}", self.cursor.segment, self.cursor.offset)?;
            file.flush()?;
        }
        fs::rename(temporary, self.cursor_path())
    }
}

enum RecordRead {
    Record { payload: Vec<u8>, consumed: u64 },
    EndOfSegment,
    Corrupt,
}

fn read_record<R: Read>(reader: &mut R) -> io::Result<RecordRead> {
    let mut header = [0u8; RECORD_HEADER_BYTES];
    match reader.read_exact(&mut header) {
        Ok(()) => {}
        Err(error) if error.kind() == io::ErrorKind::UnexpectedEof => {
            return Ok(RecordRead::EndOfSegment)
        }
        Err(error) => return Err(error),
    }

    let length = u32::from_le_bytes([header[0], header[1], header[2], header[3]]);
    let expected = u32::from_le_bytes([header[4], header[5], header[6], header[7]]);
    if length == 0 || length > MAX_RECORD_BYTES {
        return Ok(RecordRead::Corrupt);
    }

    let mut payload = vec![0u8; length as usize];
    match reader.read_exact(&mut payload) {
        Ok(()) => {}
        Err(error) if error.kind() == io::ErrorKind::UnexpectedEof => {
            return Ok(RecordRead::Corrupt)
        }
        Err(error) => return Err(error),
    }
    if crc32fast::hash(&payload) != expected {
        return Ok(RecordRead::Corrupt);
    }

    Ok(RecordRead::Record {
        payload,
        consumed: (RECORD_HEADER_BYTES + length as usize) as u64,
    })
}

fn segment_index(name: &str) -> Option<u64> {
    name.strip_prefix(SEGMENT_PREFIX)
        .and_then(|rest| rest.strip_suffix(SEGMENT_SUFFIX))
        .and_then(|digits| digits.parse().ok())
}
