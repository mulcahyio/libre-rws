# LibreDCM DICOM Conformance Statement

| | |
|---|---|
| Document | DHF-DCM-DCS |
| Status | **DRAFT for approval.** Not yet approved by Patrick Mulcahy. Not a released labeling document |
| Product | **Libre DCM** (binary `libredcm`), the DICOM archive component of Libre RWS, version **0.2** (development line `0.2.x`; no 0.2 release yet). Covers source and official builds; they behave the same on the DICOM network (licensing never affects DICOM services, DCM-SRS-058) |
| Manufacturer | Libre Systems (being formed) |
| Date | 2026-11-18 |
| Standard | DICOM PS3.2 2026d, Annex N (normative template), and the other parts of DICOM 2026d |
| Written by | Hermes (Claude), from the code and from DCMTK / pynetdicom runs against LibreDCM containers. A human reviews and approves |
| Issue | [#32](https://github.com/mulcahyio/libre-rws/issues/32) |
| Related | [SRS](SRS.md) (DCM-SRS-101), [anomalies](anomalies.md), [cybersecurity](cybersecurity.md), [UID registry](../../qms/uid-registry.md), [evidence](evidence/dcs-dcmtk/README.md) |

The tables marked *generated* are written from the running code by the Go
test `TestConformanceStatementMatchesCode`
(`modules/dicom/internal/gateway/conformance_statement_test.go`) and checked
again, independently, by `scripts/qms/dcs.py` in CI. Do not edit them by
hand: change the code, then run
`rws-go test ./modules/dicom/internal/gateway -run TestConformanceStatementMatchesCode -update-dcs`.

## 1 Overview

LibreDCM is a DICOM archive. It receives images and other composite objects
over the DICOM network (C-STORE), keeps them exactly as received, and lets
other systems find them (C-FIND) and get them back (C-MOVE, C-GET). It
answers C-ECHO and can send C-ECHO. It has one DICOM Application Entity. It
has no user interface for images: it does not display or process them.

```
   Modalities, workstations,            LibreDCM (AE title LIBREDCM by default)
   PACS, viewers                        +--------------------------------------+
   ---- C-ECHO ---------------------->  | Verification SCP                     |
   ---- C-STORE --------------------->  | Storage SCP  -> archive + index      |
   ---- C-FIND ---------------------->  | Query SCP (Patient Root, Study Root) |
   ---- C-MOVE ---------------------->  | Move SCP --+                         |
   <--- C-STORE (sub-operations) -----  |            +-> Storage SCU           |
   ---- C-GET ----------------------->  | Get SCP (C-STORE on the same assoc.) |
   <--- C-ECHO (install check, admin) - | Verification SCU                     |
                                        +--------------------------------------+
```

### 1.1 Content and Transfer

Table 1-1 lists the Storage SOP Classes LibreDCM accepts and sends, and
Table 1-2 the transfer syntaxes. LibreDCM accepts every class in Table 1-1 in
every transfer syntax in Table 1-2 (sets U, LL and L). It stores each object
in the transfer syntax it was received in and never compresses, decompresses
or converts compressed pixel data. "Archive" means LibreDCM stores the object
and makes it available again. LibreDCM does not create, display or process
instances of these classes, so those columns of the PS3.2 template are left
out. The DICOM Web and Media columns are left out because LibreDCM supports
neither (sections 1.3, 1.4).

**Table 1-1. Storage SOP Classes** (generated)

<!-- dcs:begin storage-sop-classes -->
| SOP Class | Keyword | UID | Transfer Syntax Sets | SCU | SCP | Archive |
|---|---|---|---|---|---|---|
| Computed Radiography Image Storage | `ComputedRadiographyImageStorage` | `1.2.840.10008.5.1.4.1.1.1` | U; LL; L | Y | Y | Y |
| Digital X-Ray Image Storage - For Presentation | `DigitalXRayImageStorageForPresentation` | `1.2.840.10008.5.1.4.1.1.1.1` | U; LL; L | Y | Y | Y |
| Digital X-Ray Image Storage - For Processing | `DigitalXRayImageStorageForProcessing` | `1.2.840.10008.5.1.4.1.1.1.1.1` | U; LL; L | Y | Y | Y |
| Digital Mammography X-Ray Image Storage - For Presentation | `DigitalMammographyXRayImageStorageForPresentation` | `1.2.840.10008.5.1.4.1.1.1.2` | U; LL; L | Y | Y | Y |
| Digital Mammography X-Ray Image Storage - For Processing | `DigitalMammographyXRayImageStorageForProcessing` | `1.2.840.10008.5.1.4.1.1.1.2.1` | U; LL; L | Y | Y | Y |
| Digital Intra-Oral X-Ray Image Storage - For Presentation | `DigitalIntraOralXRayImageStorageForPresentation` | `1.2.840.10008.5.1.4.1.1.1.3` | U; LL; L | Y | Y | Y |
| Digital Intra-Oral X-Ray Image Storage - For Processing | `DigitalIntraOralXRayImageStorageForProcessing` | `1.2.840.10008.5.1.4.1.1.1.3.1` | U; LL; L | Y | Y | Y |
| CT Image Storage | `CTImageStorage` | `1.2.840.10008.5.1.4.1.1.2` | U; LL; L | Y | Y | Y |
| Enhanced CT Image Storage | `EnhancedCTImageStorage` | `1.2.840.10008.5.1.4.1.1.2.1` | U; LL; L | Y | Y | Y |
| Legacy Converted Enhanced CT Image Storage | `LegacyConvertedEnhancedCTImageStorage` | `1.2.840.10008.5.1.4.1.1.2.2` | U; LL; L | Y | Y | Y |
| Ultrasound Multi-frame Image Storage | `UltrasoundMultiFrameImageStorage` | `1.2.840.10008.5.1.4.1.1.3.1` | U; LL; L | Y | Y | Y |
| MR Image Storage | `MRImageStorage` | `1.2.840.10008.5.1.4.1.1.4` | U; LL; L | Y | Y | Y |
| Enhanced MR Image Storage | `EnhancedMRImageStorage` | `1.2.840.10008.5.1.4.1.1.4.1` | U; LL; L | Y | Y | Y |
| MR Spectroscopy Storage | `MRSpectroscopyStorage` | `1.2.840.10008.5.1.4.1.1.4.2` | U; LL; L | Y | Y | Y |
| Enhanced MR Color Image Storage | `EnhancedMRColorImageStorage` | `1.2.840.10008.5.1.4.1.1.4.3` | U; LL; L | Y | Y | Y |
| Legacy Converted Enhanced MR Image Storage | `LegacyConvertedEnhancedMRImageStorage` | `1.2.840.10008.5.1.4.1.1.4.4` | U; LL; L | Y | Y | Y |
| Ultrasound Image Storage | `UltrasoundImageStorage` | `1.2.840.10008.5.1.4.1.1.6.1` | U; LL; L | Y | Y | Y |
| Enhanced US Volume Storage | `EnhancedUSVolumeStorage` | `1.2.840.10008.5.1.4.1.1.6.2` | U; LL; L | Y | Y | Y |
| Photoacoustic Image Storage | `PhotoacousticImageStorage` | `1.2.840.10008.5.1.4.1.1.6.3` | U; LL; L | Y | Y | Y |
| Secondary Capture Image Storage | `SecondaryCaptureImageStorage` | `1.2.840.10008.5.1.4.1.1.7` | U; LL; L | Y | Y | Y |
| Multi-frame Single Bit Secondary Capture Image Storage | `MultiFrameSingleBitSecondaryCaptureImageStorage` | `1.2.840.10008.5.1.4.1.1.7.1` | U; LL; L | Y | Y | Y |
| Multi-frame Grayscale Byte Secondary Capture Image Storage | `MultiFrameGrayscaleByteSecondaryCaptureImageStorage` | `1.2.840.10008.5.1.4.1.1.7.2` | U; LL; L | Y | Y | Y |
| Multi-frame Grayscale Word Secondary Capture Image Storage | `MultiFrameGrayscaleWordSecondaryCaptureImageStorage` | `1.2.840.10008.5.1.4.1.1.7.3` | U; LL; L | Y | Y | Y |
| Multi-frame True Color Secondary Capture Image Storage | `MultiFrameTrueColorSecondaryCaptureImageStorage` | `1.2.840.10008.5.1.4.1.1.7.4` | U; LL; L | Y | Y | Y |
| 12-lead ECG Waveform Storage | `TwelveLeadECGWaveformStorage` | `1.2.840.10008.5.1.4.1.1.9.1.1` | U; LL; L | Y | Y | Y |
| General ECG Waveform Storage | `GeneralECGWaveformStorage` | `1.2.840.10008.5.1.4.1.1.9.1.2` | U; LL; L | Y | Y | Y |
| Ambulatory ECG Waveform Storage | `AmbulatoryECGWaveformStorage` | `1.2.840.10008.5.1.4.1.1.9.1.3` | U; LL; L | Y | Y | Y |
| General 32-bit ECG Waveform Storage | `General32bitECGWaveformStorage` | `1.2.840.10008.5.1.4.1.1.9.1.4` | U; LL; L | Y | Y | Y |
| Hemodynamic Waveform Storage | `HemodynamicWaveformStorage` | `1.2.840.10008.5.1.4.1.1.9.2.1` | U; LL; L | Y | Y | Y |
| Cardiac Electrophysiology Waveform Storage | `CardiacElectrophysiologyWaveformStorage` | `1.2.840.10008.5.1.4.1.1.9.3.1` | U; LL; L | Y | Y | Y |
| Basic Voice Audio Waveform Storage | `BasicVoiceAudioWaveformStorage` | `1.2.840.10008.5.1.4.1.1.9.4.1` | U; LL; L | Y | Y | Y |
| General Audio Waveform Storage | `GeneralAudioWaveformStorage` | `1.2.840.10008.5.1.4.1.1.9.4.2` | U; LL; L | Y | Y | Y |
| Arterial Pulse Waveform Storage | `ArterialPulseWaveformStorage` | `1.2.840.10008.5.1.4.1.1.9.5.1` | U; LL; L | Y | Y | Y |
| Respiratory Waveform Storage | `RespiratoryWaveformStorage` | `1.2.840.10008.5.1.4.1.1.9.6.1` | U; LL; L | Y | Y | Y |
| Multi-channel Respiratory Waveform Storage | `MultichannelRespiratoryWaveformStorage` | `1.2.840.10008.5.1.4.1.1.9.6.2` | U; LL; L | Y | Y | Y |
| Routine Scalp Electroencephalogram Waveform Storage | `RoutineScalpElectroencephalogramWaveformStorage` | `1.2.840.10008.5.1.4.1.1.9.7.1` | U; LL; L | Y | Y | Y |
| Electromyogram Waveform Storage | `ElectromyogramWaveformStorage` | `1.2.840.10008.5.1.4.1.1.9.7.2` | U; LL; L | Y | Y | Y |
| Electrooculogram Waveform Storage | `ElectrooculogramWaveformStorage` | `1.2.840.10008.5.1.4.1.1.9.7.3` | U; LL; L | Y | Y | Y |
| Sleep Electroencephalogram Waveform Storage | `SleepElectroencephalogramWaveformStorage` | `1.2.840.10008.5.1.4.1.1.9.7.4` | U; LL; L | Y | Y | Y |
| Body Position Waveform Storage | `BodyPositionWaveformStorage` | `1.2.840.10008.5.1.4.1.1.9.8.1` | U; LL; L | Y | Y | Y |
| Waveform Presentation State Storage | `WaveformPresentationStateStorage` | `1.2.840.10008.5.1.4.1.1.9.100.1` | U; LL; L | Y | Y | Y |
| Waveform Acquisition Presentation State Storage | `WaveformAcquisitionPresentationStateStorage` | `1.2.840.10008.5.1.4.1.1.9.100.2` | U; LL; L | Y | Y | Y |
| Grayscale Softcopy Presentation State Storage | `GrayscaleSoftcopyPresentationStateStorage` | `1.2.840.10008.5.1.4.1.1.11.1` | U; LL; L | Y | Y | Y |
| Color Softcopy Presentation State Storage | `ColorSoftcopyPresentationStateStorage` | `1.2.840.10008.5.1.4.1.1.11.2` | U; LL; L | Y | Y | Y |
| Pseudo-Color Softcopy Presentation State Storage | `PseudoColorSoftcopyPresentationStateStorage` | `1.2.840.10008.5.1.4.1.1.11.3` | U; LL; L | Y | Y | Y |
| Blending Softcopy Presentation State Storage | `BlendingSoftcopyPresentationStateStorage` | `1.2.840.10008.5.1.4.1.1.11.4` | U; LL; L | Y | Y | Y |
| XA/XRF Grayscale Softcopy Presentation State Storage | `XAXRFGrayscaleSoftcopyPresentationStateStorage` | `1.2.840.10008.5.1.4.1.1.11.5` | U; LL; L | Y | Y | Y |
| Grayscale Planar MPR Volumetric Presentation State Storage | `GrayscalePlanarMPRVolumetricPresentationStateStorage` | `1.2.840.10008.5.1.4.1.1.11.6` | U; LL; L | Y | Y | Y |
| Compositing Planar MPR Volumetric Presentation State Storage | `CompositingPlanarMPRVolumetricPresentationStateStorage` | `1.2.840.10008.5.1.4.1.1.11.7` | U; LL; L | Y | Y | Y |
| Advanced Blending Presentation State Storage | `AdvancedBlendingPresentationStateStorage` | `1.2.840.10008.5.1.4.1.1.11.8` | U; LL; L | Y | Y | Y |
| Volume Rendering Volumetric Presentation State Storage | `VolumeRenderingVolumetricPresentationStateStorage` | `1.2.840.10008.5.1.4.1.1.11.9` | U; LL; L | Y | Y | Y |
| Segmented Volume Rendering Volumetric Presentation State Storage | `SegmentedVolumeRenderingVolumetricPresentationStateStorage` | `1.2.840.10008.5.1.4.1.1.11.10` | U; LL; L | Y | Y | Y |
| Multiple Volume Rendering Volumetric Presentation State Storage | `MultipleVolumeRenderingVolumetricPresentationStateStorage` | `1.2.840.10008.5.1.4.1.1.11.11` | U; LL; L | Y | Y | Y |
| Variable Modality LUT Softcopy Presentation State Storage | `VariableModalityLUTSoftcopyPresentationStateStorage` | `1.2.840.10008.5.1.4.1.1.11.12` | U; LL; L | Y | Y | Y |
| X-Ray Angiographic Image Storage | `XRayAngiographicImageStorage` | `1.2.840.10008.5.1.4.1.1.12.1` | U; LL; L | Y | Y | Y |
| Enhanced XA Image Storage | `EnhancedXAImageStorage` | `1.2.840.10008.5.1.4.1.1.12.1.1` | U; LL; L | Y | Y | Y |
| X-Ray Radiofluoroscopic Image Storage | `XRayRadiofluoroscopicImageStorage` | `1.2.840.10008.5.1.4.1.1.12.2` | U; LL; L | Y | Y | Y |
| Enhanced XRF Image Storage | `EnhancedXRFImageStorage` | `1.2.840.10008.5.1.4.1.1.12.2.1` | U; LL; L | Y | Y | Y |
| X-Ray 3D Angiographic Image Storage | `XRay3DAngiographicImageStorage` | `1.2.840.10008.5.1.4.1.1.13.1.1` | U; LL; L | Y | Y | Y |
| X-Ray 3D Craniofacial Image Storage | `XRay3DCraniofacialImageStorage` | `1.2.840.10008.5.1.4.1.1.13.1.2` | U; LL; L | Y | Y | Y |
| Breast Tomosynthesis Image Storage | `BreastTomosynthesisImageStorage` | `1.2.840.10008.5.1.4.1.1.13.1.3` | U; LL; L | Y | Y | Y |
| Breast Projection X-Ray Image Storage - For Presentation | `BreastProjectionXRayImageStorageForPresentation` | `1.2.840.10008.5.1.4.1.1.13.1.4` | U; LL; L | Y | Y | Y |
| Breast Projection X-Ray Image Storage - For Processing | `BreastProjectionXRayImageStorageForProcessing` | `1.2.840.10008.5.1.4.1.1.13.1.5` | U; LL; L | Y | Y | Y |
| Intravascular Optical Coherence Tomography Image Storage - For Presentation | `IntravascularOpticalCoherenceTomographyImageStorageForPresentation` | `1.2.840.10008.5.1.4.1.1.14.1` | U; LL; L | Y | Y | Y |
| Intravascular Optical Coherence Tomography Image Storage - For Processing | `IntravascularOpticalCoherenceTomographyImageStorageForProcessing` | `1.2.840.10008.5.1.4.1.1.14.2` | U; LL; L | Y | Y | Y |
| Nuclear Medicine Image Storage | `NuclearMedicineImageStorage` | `1.2.840.10008.5.1.4.1.1.20` | U; LL; L | Y | Y | Y |
| Parametric Map Storage | `ParametricMapStorage` | `1.2.840.10008.5.1.4.1.1.30` | U; LL; L | Y | Y | Y |
| Raw Data Storage | `RawDataStorage` | `1.2.840.10008.5.1.4.1.1.66` | U; LL; L | Y | Y | Y |
| Spatial Registration Storage | `SpatialRegistrationStorage` | `1.2.840.10008.5.1.4.1.1.66.1` | U; LL; L | Y | Y | Y |
| Spatial Fiducials Storage | `SpatialFiducialsStorage` | `1.2.840.10008.5.1.4.1.1.66.2` | U; LL; L | Y | Y | Y |
| Deformable Spatial Registration Storage | `DeformableSpatialRegistrationStorage` | `1.2.840.10008.5.1.4.1.1.66.3` | U; LL; L | Y | Y | Y |
| Segmentation Storage | `SegmentationStorage` | `1.2.840.10008.5.1.4.1.1.66.4` | U; LL; L | Y | Y | Y |
| Surface Segmentation Storage | `SurfaceSegmentationStorage` | `1.2.840.10008.5.1.4.1.1.66.5` | U; LL; L | Y | Y | Y |
| Tractography Results Storage | `TractographyResultsStorage` | `1.2.840.10008.5.1.4.1.1.66.6` | U; LL; L | Y | Y | Y |
| Label Map Segmentation Storage | `LabelMapSegmentationStorage` | `1.2.840.10008.5.1.4.1.1.66.7` | U; LL; L | Y | Y | Y |
| Height Map Segmentation Storage | `HeightMapSegmentationStorage` | `1.2.840.10008.5.1.4.1.1.66.8` | U; LL; L | Y | Y | Y |
| Real World Value Mapping Storage | `RealWorldValueMappingStorage` | `1.2.840.10008.5.1.4.1.1.67` | U; LL; L | Y | Y | Y |
| Surface Scan Mesh Storage | `SurfaceScanMeshStorage` | `1.2.840.10008.5.1.4.1.1.68.1` | U; LL; L | Y | Y | Y |
| Surface Scan Point Cloud Storage | `SurfaceScanPointCloudStorage` | `1.2.840.10008.5.1.4.1.1.68.2` | U; LL; L | Y | Y | Y |
| VL Endoscopic Image Storage | `VLEndoscopicImageStorage` | `1.2.840.10008.5.1.4.1.1.77.1.1` | U; LL; L | Y | Y | Y |
| Video Endoscopic Image Storage | `VideoEndoscopicImageStorage` | `1.2.840.10008.5.1.4.1.1.77.1.1.1` | U; LL; L | Y | Y | Y |
| VL Microscopic Image Storage | `VLMicroscopicImageStorage` | `1.2.840.10008.5.1.4.1.1.77.1.2` | U; LL; L | Y | Y | Y |
| Video Microscopic Image Storage | `VideoMicroscopicImageStorage` | `1.2.840.10008.5.1.4.1.1.77.1.2.1` | U; LL; L | Y | Y | Y |
| VL Slide-Coordinates Microscopic Image Storage | `VLSlideCoordinatesMicroscopicImageStorage` | `1.2.840.10008.5.1.4.1.1.77.1.3` | U; LL; L | Y | Y | Y |
| VL Photographic Image Storage | `VLPhotographicImageStorage` | `1.2.840.10008.5.1.4.1.1.77.1.4` | U; LL; L | Y | Y | Y |
| Video Photographic Image Storage | `VideoPhotographicImageStorage` | `1.2.840.10008.5.1.4.1.1.77.1.4.1` | U; LL; L | Y | Y | Y |
| Ophthalmic Photography 8 Bit Image Storage | `OphthalmicPhotography8BitImageStorage` | `1.2.840.10008.5.1.4.1.1.77.1.5.1` | U; LL; L | Y | Y | Y |
| Ophthalmic Photography 16 Bit Image Storage | `OphthalmicPhotography16BitImageStorage` | `1.2.840.10008.5.1.4.1.1.77.1.5.2` | U; LL; L | Y | Y | Y |
| Stereometric Relationship Storage | `StereometricRelationshipStorage` | `1.2.840.10008.5.1.4.1.1.77.1.5.3` | U; LL; L | Y | Y | Y |
| Ophthalmic Tomography Image Storage | `OphthalmicTomographyImageStorage` | `1.2.840.10008.5.1.4.1.1.77.1.5.4` | U; LL; L | Y | Y | Y |
| Wide Field Ophthalmic Photography Stereographic Projection Image Storage | `WideFieldOphthalmicPhotographyStereographicProjectionImageStorage` | `1.2.840.10008.5.1.4.1.1.77.1.5.5` | U; LL; L | Y | Y | Y |
| Wide Field Ophthalmic Photography 3D Coordinates Image Storage | `WideFieldOphthalmicPhotography3DCoordinatesImageStorage` | `1.2.840.10008.5.1.4.1.1.77.1.5.6` | U; LL; L | Y | Y | Y |
| Ophthalmic Optical Coherence Tomography En Face Image Storage | `OphthalmicOpticalCoherenceTomographyEnFaceImageStorage` | `1.2.840.10008.5.1.4.1.1.77.1.5.7` | U; LL; L | Y | Y | Y |
| Ophthalmic Optical Coherence Tomography B-scan Volume Analysis Storage | `OphthalmicOpticalCoherenceTomographyBscanVolumeAnalysisStorage` | `1.2.840.10008.5.1.4.1.1.77.1.5.8` | U; LL; L | Y | Y | Y |
| VL Whole Slide Microscopy Image Storage | `VLWholeSlideMicroscopyImageStorage` | `1.2.840.10008.5.1.4.1.1.77.1.6` | U; LL; L | Y | Y | Y |
| Dermoscopic Photography Image Storage | `DermoscopicPhotographyImageStorage` | `1.2.840.10008.5.1.4.1.1.77.1.7` | U; LL; L | Y | Y | Y |
| Confocal Microscopy Image Storage | `ConfocalMicroscopyImageStorage` | `1.2.840.10008.5.1.4.1.1.77.1.8` | U; LL; L | Y | Y | Y |
| Confocal Microscopy Tiled Pyramidal Image Storage | `ConfocalMicroscopyTiledPyramidalImageStorage` | `1.2.840.10008.5.1.4.1.1.77.1.9` | U; LL; L | Y | Y | Y |
| Lensometry Measurements Storage | `LensometryMeasurementsStorage` | `1.2.840.10008.5.1.4.1.1.78.1` | U; LL; L | Y | Y | Y |
| Autorefraction Measurements Storage | `AutorefractionMeasurementsStorage` | `1.2.840.10008.5.1.4.1.1.78.2` | U; LL; L | Y | Y | Y |
| Keratometry Measurements Storage | `KeratometryMeasurementsStorage` | `1.2.840.10008.5.1.4.1.1.78.3` | U; LL; L | Y | Y | Y |
| Subjective Refraction Measurements Storage | `SubjectiveRefractionMeasurementsStorage` | `1.2.840.10008.5.1.4.1.1.78.4` | U; LL; L | Y | Y | Y |
| Visual Acuity Measurements Storage | `VisualAcuityMeasurementsStorage` | `1.2.840.10008.5.1.4.1.1.78.5` | U; LL; L | Y | Y | Y |
| Spectacle Prescription Report Storage | `SpectaclePrescriptionReportStorage` | `1.2.840.10008.5.1.4.1.1.78.6` | U; LL; L | Y | Y | Y |
| Ophthalmic Axial Measurements Storage | `OphthalmicAxialMeasurementsStorage` | `1.2.840.10008.5.1.4.1.1.78.7` | U; LL; L | Y | Y | Y |
| Intraocular Lens Calculations Storage | `IntraocularLensCalculationsStorage` | `1.2.840.10008.5.1.4.1.1.78.8` | U; LL; L | Y | Y | Y |
| Macular Grid Thickness and Volume Report Storage | `MacularGridThicknessAndVolumeReportStorage` | `1.2.840.10008.5.1.4.1.1.79.1` | U; LL; L | Y | Y | Y |
| Ophthalmic Visual Field Static Perimetry Measurements Storage | `OphthalmicVisualFieldStaticPerimetryMeasurementsStorage` | `1.2.840.10008.5.1.4.1.1.80.1` | U; LL; L | Y | Y | Y |
| Ophthalmic Thickness Map Storage | `OphthalmicThicknessMapStorage` | `1.2.840.10008.5.1.4.1.1.81.1` | U; LL; L | Y | Y | Y |
| Corneal Topography Map Storage | `CornealTopographyMapStorage` | `1.2.840.10008.5.1.4.1.1.82.1` | U; LL; L | Y | Y | Y |
| Basic Text SR Storage | `BasicTextSRStorage` | `1.2.840.10008.5.1.4.1.1.88.11` | U; LL; L | Y | Y | Y |
| Enhanced SR Storage | `EnhancedSRStorage` | `1.2.840.10008.5.1.4.1.1.88.22` | U; LL; L | Y | Y | Y |
| Comprehensive SR Storage | `ComprehensiveSRStorage` | `1.2.840.10008.5.1.4.1.1.88.33` | U; LL; L | Y | Y | Y |
| Comprehensive 3D SR Storage | `Comprehensive3DSRStorage` | `1.2.840.10008.5.1.4.1.1.88.34` | U; LL; L | Y | Y | Y |
| Extensible SR Storage | `ExtensibleSRStorage` | `1.2.840.10008.5.1.4.1.1.88.35` | U; LL; L | Y | Y | Y |
| Procedure Log Storage | `ProcedureLogStorage` | `1.2.840.10008.5.1.4.1.1.88.40` | U; LL; L | Y | Y | Y |
| Mammography CAD SR Storage | `MammographyCADSRStorage` | `1.2.840.10008.5.1.4.1.1.88.50` | U; LL; L | Y | Y | Y |
| Key Object Selection Document Storage | `KeyObjectSelectionDocumentStorage` | `1.2.840.10008.5.1.4.1.1.88.59` | U; LL; L | Y | Y | Y |
| Chest CAD SR Storage | `ChestCADSRStorage` | `1.2.840.10008.5.1.4.1.1.88.65` | U; LL; L | Y | Y | Y |
| X-Ray Radiation Dose SR Storage | `XRayRadiationDoseSRStorage` | `1.2.840.10008.5.1.4.1.1.88.67` | U; LL; L | Y | Y | Y |
| Radiopharmaceutical Radiation Dose SR Storage | `RadiopharmaceuticalRadiationDoseSRStorage` | `1.2.840.10008.5.1.4.1.1.88.68` | U; LL; L | Y | Y | Y |
| Colon CAD SR Storage | `ColonCADSRStorage` | `1.2.840.10008.5.1.4.1.1.88.69` | U; LL; L | Y | Y | Y |
| Implantation Plan SR Storage | `ImplantationPlanSRStorage` | `1.2.840.10008.5.1.4.1.1.88.70` | U; LL; L | Y | Y | Y |
| Acquisition Context SR Storage | `AcquisitionContextSRStorage` | `1.2.840.10008.5.1.4.1.1.88.71` | U; LL; L | Y | Y | Y |
| Simplified Adult Echo SR Storage | `SimplifiedAdultEchoSRStorage` | `1.2.840.10008.5.1.4.1.1.88.72` | U; LL; L | Y | Y | Y |
| Patient Radiation Dose SR Storage | `PatientRadiationDoseSRStorage` | `1.2.840.10008.5.1.4.1.1.88.73` | U; LL; L | Y | Y | Y |
| Planned Imaging Agent Administration SR Storage | `PlannedImagingAgentAdministrationSRStorage` | `1.2.840.10008.5.1.4.1.1.88.74` | U; LL; L | Y | Y | Y |
| Performed Imaging Agent Administration SR Storage | `PerformedImagingAgentAdministrationSRStorage` | `1.2.840.10008.5.1.4.1.1.88.75` | U; LL; L | Y | Y | Y |
| Enhanced X-Ray Radiation Dose SR Storage | `EnhancedXRayRadiationDoseSRStorage` | `1.2.840.10008.5.1.4.1.1.88.76` | U; LL; L | Y | Y | Y |
| Waveform Annotation SR Storage | `WaveformAnnotationSRStorage` | `1.2.840.10008.5.1.4.1.1.88.77` | U; LL; L | Y | Y | Y |
| Content Assessment Results Storage | `ContentAssessmentResultsStorage` | `1.2.840.10008.5.1.4.1.1.90.1` | U; LL; L | Y | Y | Y |
| Microscopy Bulk Simple Annotations Storage | `MicroscopyBulkSimpleAnnotationsStorage` | `1.2.840.10008.5.1.4.1.1.91.1` | U; LL; L | Y | Y | Y |
| Encapsulated PDF Storage | `EncapsulatedPDFStorage` | `1.2.840.10008.5.1.4.1.1.104.1` | U; LL; L | Y | Y | Y |
| Encapsulated CDA Storage | `EncapsulatedCDAStorage` | `1.2.840.10008.5.1.4.1.1.104.2` | U; LL; L | Y | Y | Y |
| Encapsulated STL Storage | `EncapsulatedSTLStorage` | `1.2.840.10008.5.1.4.1.1.104.3` | U; LL; L | Y | Y | Y |
| Encapsulated OBJ Storage | `EncapsulatedOBJStorage` | `1.2.840.10008.5.1.4.1.1.104.4` | U; LL; L | Y | Y | Y |
| Encapsulated MTL Storage | `EncapsulatedMTLStorage` | `1.2.840.10008.5.1.4.1.1.104.5` | U; LL; L | Y | Y | Y |
| Positron Emission Tomography Image Storage | `PositronEmissionTomographyImageStorage` | `1.2.840.10008.5.1.4.1.1.128` | U; LL; L | Y | Y | Y |
| Legacy Converted Enhanced PET Image Storage | `LegacyConvertedEnhancedPETImageStorage` | `1.2.840.10008.5.1.4.1.1.128.1` | U; LL; L | Y | Y | Y |
| Enhanced PET Image Storage | `EnhancedPETImageStorage` | `1.2.840.10008.5.1.4.1.1.130` | U; LL; L | Y | Y | Y |
| Basic Structured Display Storage | `BasicStructuredDisplayStorage` | `1.2.840.10008.5.1.4.1.1.131` | U; LL; L | Y | Y | Y |
| CT Performed Procedure Protocol Storage | `CTPerformedProcedureProtocolStorage` | `1.2.840.10008.5.1.4.1.1.200.2` | U; LL; L | Y | Y | Y |
| XA Performed Procedure Protocol Storage | `XAPerformedProcedureProtocolStorage` | `1.2.840.10008.5.1.4.1.1.200.8` | U; LL; L | Y | Y | Y |
| RT Image Storage | `RTImageStorage` | `1.2.840.10008.5.1.4.1.1.481.1` | U; LL; L | Y | Y | Y |
| RT Dose Storage | `RTDoseStorage` | `1.2.840.10008.5.1.4.1.1.481.2` | U; LL; L | Y | Y | Y |
| RT Structure Set Storage | `RTStructureSetStorage` | `1.2.840.10008.5.1.4.1.1.481.3` | U; LL; L | Y | Y | Y |
| RT Beams Treatment Record Storage | `RTBeamsTreatmentRecordStorage` | `1.2.840.10008.5.1.4.1.1.481.4` | U; LL; L | Y | Y | Y |
| RT Plan Storage | `RTPlanStorage` | `1.2.840.10008.5.1.4.1.1.481.5` | U; LL; L | Y | Y | Y |
| RT Brachy Treatment Record Storage | `RTBrachyTreatmentRecordStorage` | `1.2.840.10008.5.1.4.1.1.481.6` | U; LL; L | Y | Y | Y |
| RT Treatment Summary Record Storage | `RTTreatmentSummaryRecordStorage` | `1.2.840.10008.5.1.4.1.1.481.7` | U; LL; L | Y | Y | Y |
| RT Ion Plan Storage | `RTIonPlanStorage` | `1.2.840.10008.5.1.4.1.1.481.8` | U; LL; L | Y | Y | Y |
| RT Ion Beams Treatment Record Storage | `RTIonBeamsTreatmentRecordStorage` | `1.2.840.10008.5.1.4.1.1.481.9` | U; LL; L | Y | Y | Y |
| RT Physician Intent Storage | `RTPhysicianIntentStorage` | `1.2.840.10008.5.1.4.1.1.481.10` | U; LL; L | Y | Y | Y |
| RT Segment Annotation Storage | `RTSegmentAnnotationStorage` | `1.2.840.10008.5.1.4.1.1.481.11` | U; LL; L | Y | Y | Y |
| RT Radiation Set Storage | `RTRadiationSetStorage` | `1.2.840.10008.5.1.4.1.1.481.12` | U; LL; L | Y | Y | Y |
| C-Arm Photon-Electron Radiation Storage | `CArmPhotonElectronRadiationStorage` | `1.2.840.10008.5.1.4.1.1.481.13` | U; LL; L | Y | Y | Y |
| Tomotherapeutic Radiation Storage | `TomotherapeuticRadiationStorage` | `1.2.840.10008.5.1.4.1.1.481.14` | U; LL; L | Y | Y | Y |
| Robotic-Arm Radiation Storage | `RoboticArmRadiationStorage` | `1.2.840.10008.5.1.4.1.1.481.15` | U; LL; L | Y | Y | Y |
| RT Radiation Record Set Storage | `RTRadiationRecordSetStorage` | `1.2.840.10008.5.1.4.1.1.481.16` | U; LL; L | Y | Y | Y |
| RT Radiation Salvage Record Storage | `RTRadiationSalvageRecordStorage` | `1.2.840.10008.5.1.4.1.1.481.17` | U; LL; L | Y | Y | Y |
| Tomotherapeutic Radiation Record Storage | `TomotherapeuticRadiationRecordStorage` | `1.2.840.10008.5.1.4.1.1.481.18` | U; LL; L | Y | Y | Y |
| C-Arm Photon-Electron Radiation Record Storage | `CArmPhotonElectronRadiationRecordStorage` | `1.2.840.10008.5.1.4.1.1.481.19` | U; LL; L | Y | Y | Y |
| Robotic Radiation Record Storage | `RoboticRadiationRecordStorage` | `1.2.840.10008.5.1.4.1.1.481.20` | U; LL; L | Y | Y | Y |
| RT Radiation Set Delivery Instruction Storage | `RTRadiationSetDeliveryInstructionStorage` | `1.2.840.10008.5.1.4.1.1.481.21` | U; LL; L | Y | Y | Y |
| RT Treatment Preparation Storage | `RTTreatmentPreparationStorage` | `1.2.840.10008.5.1.4.1.1.481.22` | U; LL; L | Y | Y | Y |
| Enhanced RT Image Storage | `EnhancedRTImageStorage` | `1.2.840.10008.5.1.4.1.1.481.23` | U; LL; L | Y | Y | Y |
| Enhanced Continuous RT Image Storage | `EnhancedContinuousRTImageStorage` | `1.2.840.10008.5.1.4.1.1.481.24` | U; LL; L | Y | Y | Y |
| RT Patient Position Acquisition Instruction Storage | `RTPatientPositionAcquisitionInstructionStorage` | `1.2.840.10008.5.1.4.1.1.481.25` | U; LL; L | Y | Y | Y |
| RT Beams Delivery Instruction Storage | `RTBeamsDeliveryInstructionStorage` | `1.2.840.10008.5.1.4.34.7` | U; LL; L | Y | Y | Y |
| RT Brachy Application Setup Delivery Instruction Storage | `RTBrachyApplicationSetupDeliveryInstructionStorage` | `1.2.840.10008.5.1.4.34.10` | U; LL; L | Y | Y | Y |
<!-- dcs:end storage-sop-classes -->

With `accept_any_sop_class: true` (off by default) LibreDCM also accepts any
other proposed abstract syntax, including retired and private SOP classes,
and stores such objects like the classes above (section 5.2.5.2). Without it,
retired Storage SOP Classes (for example Ultrasound Image Storage (Retired))
are refused (evidence 38).

**Table 1-2. Supported Transfer Syntaxes** (generated)

<!-- dcs:begin transfer-syntaxes -->
| Transfer Syntax Set | Transfer Syntax Name | UID |
|---|---|---|
| Uncompressed (U) | Explicit VR Little Endian | `1.2.840.10008.1.2.1` |
| Uncompressed (U) | Implicit VR Little Endian | `1.2.840.10008.1.2` |
| Lossless compressed (LL) | JPEG Lossless, Non-Hierarchical (Process 14) | `1.2.840.10008.1.2.4.57` |
| Lossless compressed (LL) | JPEG Lossless, Non-Hierarchical, First-Order Prediction (Process 14 [Selection Value 1]) | `1.2.840.10008.1.2.4.70` |
| Lossless compressed (LL) | JPEG-LS Lossless Image Compression | `1.2.840.10008.1.2.4.80` |
| Lossless compressed (LL) | JPEG 2000 Image Compression (Lossless Only) | `1.2.840.10008.1.2.4.90` |
| Lossless compressed (LL) | High-Throughput JPEG 2000 Image Compression (Lossless Only) | `1.2.840.10008.1.2.4.201` |
| Lossless compressed (LL) | High-Throughput JPEG 2000 with RPCL Options Image Compression (Lossless Only) | `1.2.840.10008.1.2.4.202` |
| Lossless compressed (LL) | RLE Lossless | `1.2.840.10008.1.2.5` |
| Lossy compressed (L) | JPEG Baseline (Process 1) | `1.2.840.10008.1.2.4.50` |
| Lossy compressed (L) | JPEG Extended (Process 2 and 4) | `1.2.840.10008.1.2.4.51` |
| Lossy compressed (L) | JPEG-LS Lossy (Near-Lossless) Image Compression | `1.2.840.10008.1.2.4.81` |
| Lossy compressed (L) | JPEG 2000 Image Compression | `1.2.840.10008.1.2.4.91` |
| Lossy compressed (L) | High-Throughput JPEG 2000 Image Compression | `1.2.840.10008.1.2.4.203` |
<!-- dcs:end transfer-syntaxes -->

Explicit VR Big Endian (`1.2.840.10008.1.2.2`, retired), Deflated Explicit VR
Little Endian (`1.2.840.10008.1.2.1.99`), the MPEG/HEVC video syntaxes and
all other syntaxes not in Table 1-2 are not accepted (evidence 16, 60).

#### 1.1.1 Structured Reporting Root Template IDs - N/A

N/A. LibreDCM stores SR objects (Table 1-1) without reading their content.

### 1.2 DIMSE Services

**Table 1-3. Verification and Query/Retrieve SOP Classes** (generated)

<!-- dcs:begin dimse-sop-classes -->
| SOP Class | Keyword | UID | SCU | SCP |
|---|---|---|---|---|
| Verification SOP Class | `Verification` | `1.2.840.10008.1.1` | Y | Y |
| Patient Root Query/Retrieve Information Model - FIND | `PatientRootQueryRetrieveInformationModelFind` | `1.2.840.10008.5.1.4.1.2.1.1` | N | Y |
| Study Root Query/Retrieve Information Model - FIND | `StudyRootQueryRetrieveInformationModelFind` | `1.2.840.10008.5.1.4.1.2.2.1` | N | Y |
| Patient Root Query/Retrieve Information Model - MOVE | `PatientRootQueryRetrieveInformationModelMove` | `1.2.840.10008.5.1.4.1.2.1.2` | N | Y |
| Study Root Query/Retrieve Information Model - MOVE | `StudyRootQueryRetrieveInformationModelMove` | `1.2.840.10008.5.1.4.1.2.2.2` | N | Y |
| Patient Root Query/Retrieve Information Model - GET | `PatientRootQueryRetrieveInformationModelGet` | `1.2.840.10008.5.1.4.1.2.1.3` | N | Y |
| Study Root Query/Retrieve Information Model - GET | `StudyRootQueryRetrieveInformationModelGet` | `1.2.840.10008.5.1.4.1.2.2.3` | N | Y |
<!-- dcs:end dimse-sop-classes -->

#### 1.2.1 Verification

LibreDCM is an SCP of the Verification SOP Class on every association, and an
SCU for its install check (`libredcm -echo HOST:PORT`) and for the
administrator's connection test (control API `POST /control/v1/echo`).

#### 1.2.2 Storage

See Table 1-1. LibreDCM is a Storage SCP. It is a Storage SCU only to send
stored objects: C-MOVE and C-GET sub-operations, and the administrator's
"send study" action (control API `POST /control/v1/send`), which uses the
C-MOVE send path.

#### 1.2.3 Workflow Management - N/A

N/A. No Modality Worklist, MPPS, Storage Commitment, UPS or Instance
Availability Notification.

#### 1.2.4 Query/Retrieve

See Table 1-3. LibreDCM is an SCP of the Patient Root and Study Root
Query/Retrieve Information Models for FIND, MOVE and GET. It is not a
Query/Retrieve SCU. The Patient/Study Only model, Composite Instance Root
Retrieve and the Composite Instance Retrieve Without Bulk Data classes are not
supported.

#### 1.2.5 Printing - N/A

N/A.

### 1.3 DICOM Web Services - N/A

N/A. LibreDCM has no WADO-URI, WADO-RS, STOW-RS or QIDO-RS service. Its
HTTP admin endpoint (health, metrics, version, control API) is not a DICOM Web
service.

### 1.4 Media Services - N/A

N/A. LibreDCM does not read or write DICOM media (no File-set Creator, Reader
or Updater). Its archive holds DICOM Part 10 files, but the archive is not a
DICOM File-set and has no DICOMDIR.

### 1.5 Real-Time Video Service - N/A

N/A.

### 1.6 De-identification Profiles - N/A

N/A. LibreDCM does not de-identify. It can leave Patient IDs out of its event
messages and audit records (`events_omit_patient_id`,
`audit_omit_patient_id`); that is not a PS3.15 de-identification profile.

### 1.7 Specific Character Sets

LibreDCM stores objects with whatever Specific Character Set (0008,0005) they
carry, unchanged. It decodes text for its index and for C-FIND matching, and
answers C-FIND in ISO_IR 192 (UTF-8) when a returned value is not ASCII.
Details: section 5.7.

## 2 Table of Contents

1. [Overview](#1-overview)
2. [Table of Contents](#2-table-of-contents)
3. [Introduction](#3-introduction) (revision history, audience, remarks,
   known deviations, verification of this statement, terms, abbreviations,
   references)
4. [Implementation Model](#4-implementation-model)
5. [Service and Interoperability Description](#5-service-and-interoperability-description)
6. [Configuration](#6-configuration)
7. [Network and Media Communication Details](#7-network-and-media-communication-details)
8. [Security](#8-security)
9. [Information Object Definitions](#9-information-object-definitions)
10. [Structured Report Content Encoding](#10-structured-report-content-encoding---na)
11. [Security Details](#11-security-details)
12. [Mapping of Attributes](#12-mapping-of-attributes---na)

## 3 Introduction

### 3.1 Revision History

| Version | Date | Product version | Change |
|---|---|---|---|
| 0.1 (DRAFT) | 2026-11-18 | LibreDCM 0.2 (development) | First version (issue #32), written after the Libre Systems UID root (issue #30) gave LibreDCM its own Implementation Class UID |

### 3.2 Audience

Engineers who connect LibreDCM to modalities, workstations, PACS and viewers;
integration and IHE testers; the people who validate a LibreDCM installation;
and regulatory reviewers. The reader should know DICOM networking (PS3.4,
PS3.7, PS3.8).

### 3.3 Remarks

- This statement is a **draft**. LibreDCM 0.2 is not released. Read it with
  the release notes of the version you install.
- **Regulatory status.** LibreDCM on its own only receives, stores and sends
  medical images. It is a non-device software function under FD&C Act
  §520(o)(1)(D) and is not FDA cleared. It is developed under the Libre RWS
  quality system for a future system that adds a diagnostic viewer (see
  [intended-use.md](intended-use.md)).
- A conformance statement does not replace testing. Test LibreDCM with the
  systems it will connect to before clinical use.
- LibreDCM makes **no IHE claims** in this version. An IHE Integration
  Statement is left for the IHE work.
- Several LibreDCM nodes can run as one archive behind a TCP load balancer
  (shared index and storage). Every node answers with the same AE title and
  behaves as described here; any node serves objects stored by any other node
  (DCM-SRS-055).

#### 3.3.1 Known deviations and limitations

Listed so that integrators can plan around them. Each has an entry with a
safety assessment in [anomalies.md](anomalies.md) or is a design limit
described in this statement.

| # | Deviation or limitation | Effect for the peer | Record |
|---|---|---|---|
| 1 | **C-GET with a storage context missing.** If a matched instance belongs to a SOP class for which the C-GET requestor has no accepted storage presentation context, LibreDCM stops: no further C-STORE, no final C-GET response, and the connection is closed. The export audit record is written for all matched instances as if they were sent | The requestor gets part of the data and no final status; the audit record overstates what was sent. Offer storage contexts for every class you retrieve, or use C-MOVE | ANOM-011; evidence 60 |
| 2 | **C-GET counts.** The final C-GET response counts every C-STORE sent as Completed, whatever status the requestor returned | Misleading counts; the requestor knows its own results | ANOM-010 |
| 3 | **C-GET of compressed objects.** C-GET sends only objects stored in Implicit or Explicit VR Little Endian. Objects stored compressed are failed sub-operations (Failed SOP Instance UID List). A C-GET whose objects exceed `get_max_bytes` (default 1 GiB) is refused as a whole with A702 | Use C-MOVE for compressed objects and very large retrievals | design limit; evidence 39 |
| 4 | **C-CANCEL during C-MOVE and C-GET** is not acted on: LibreDCM finishes all sub-operations (PS3.4 C.4.2.3.1 asks the SCP to interrupt them). No Pending responses are sent during C-MOVE or C-GET (optional in PS3.4) | A cancelled retrieve runs to the end; no progress information | ANOM-013 |
| 5 | **C-FIND result limit.** When a query has more matches than `find_limit` (default 1000), LibreDCM returns the first `find_limit` matches and then Success, with no indication to the peer that matches were left out (only a warning in its log) | The user may believe the list is complete. Narrow the query, or raise `find_limit` | ANOM-012; evidence 31 |
| 6 | **Rejections for a wrong Called AE Title are logged but not audited.** Only allow-list rejections produce a `SecurityAlert.association-rejected` audit record | Less complete security audit trail | ANOM-014; evidence 11, 13, 80 |
| 7 | **Hierarchical query checks are lenient.** LibreDCM does not require the unique keys of the higher levels (for example Patient ID in a Patient Root STUDY query); it matches the keys it gets. Relational queries are not negotiated | A query that a strict SCP would refuse is answered | design; section 5.2.7.5 |
| 8 | **Only the first value of a multi-valued matching key is used**, except for UID list matching and Modalities in Study | Other values are ignored | design; section 5.2.7.5 |
| 9 | **Patient conflicts.** By default an instance whose Study Instance UID is already archived under another Patient ID (or whose Series or SOP Instance UID belongs to another study) is stored, held and hidden from query and retrieve until an administrator resolves it, and the sender gets Success. With `patient_conflict_policy: reject` it is refused with A778; with `patient_conflict_coerce_patient_id: true` its Patient ID is coerced and the sender gets Warning B000 | The sender may not see that the instance is held. This is the designed control for wrong-patient association (HAZ-013). The earlier silent reassignment (ANOM-002, ANOM-009) is fixed | DCM-SRS-070…079; evidence 19, 20, 21, 30 |
| 10 | Part 10 files are written without (0002,0016) Source Application Entity Title; the calling AE title is recorded in the index instead | None on the network | ANOM-006 |

Interoperability note (not a LibreDCM deviation): DCMTK 3.6.7 `getscu` does
not read the identifier (Failed SOP Instance UID List) that LibreDCM attaches
to a failed final C-GET response, so its association release then fails with
"DUL P-Data PDU arrived" (evidence 39). `movescu` reads the same identifier
without problems (evidence 33).

#### 3.3.2 Verification of this statement

| What | How | Record |
|---|---|---|
| Tables 1-1, 1-2, 1-3, 5-5 to 5-8, 6-1 and 7-1 | Generated from the running code by `TestConformanceStatementMatchesCode` (names and tags from the godicom dictionary; "returned" keys found by a real C-FIND at each level); fails when the document differs | DCM-SRS-101 |
| The same tables, independently | `scripts/qms/dcs.py` reads the Go source as text (no shared code) and fails the CI `verify` job on any difference or a missing section | QMS-SOP-011 tool; `scripts/qms/test_qms_tools.py` |
| Every behaviour statement | DCMTK 3.6.7 (`echoscu`, `storescu`, `dcmsend`, `findscu`, `movescu`, `getscu`, `dcmdump`) and pynetdicom 3.0.4 probes against five LibreDCM containers built from this branch | [evidence/dcs-dcmtk](evidence/dcs-dcmtk/README.md); "evidence NN" in this document means log file `NN-*.log` there |

### 3.4 Terms and Definitions

Terms are used as defined in DICOM PS3.1 to PS3.20. In addition:

- **Archive**: the storage (filesystem or S3) and the index (SQLite or
  PostgreSQL) that LibreDCM keeps.
- **Held instance**: an instance stored under a patient-conflict policy and
  not returned by query or retrieve until an administrator resolves the
  conflict.
- **Control API**: LibreDCM's administrative REST API on its HTTP admin
  endpoint, used by Libre WT. It is not a DICOM service.

### 3.5 Abbreviations

AE Application Entity; CIDR Classless Inter-Domain Routing; DIMSE DICOM
Message Service Element; IOD Information Object Definition; PDU Protocol Data
Unit; SCP Service Class Provider; SCU Service Class User; SOP Service-Object
Pair; TLS Transport Layer Security; UID Unique Identifier.

### 3.6 References

- DICOM PS3.2, PS3.4, PS3.5, PS3.6, PS3.7, PS3.8, PS3.10, PS3.15, edition
  2026d (<https://dicom.nema.org/medical/dicom/current/>).
- RFC 8446 (TLS 1.3), RFC 5246 (TLS 1.2), BCP 195.
- LibreDCM design file: [SRS.md](SRS.md), [architecture.md](architecture.md),
  [risk-management.md](risk-management.md),
  [cybersecurity.md](cybersecurity.md), [anomalies.md](anomalies.md).
- Libre Systems UID registry: [docs/qms/uid-registry.md](../../qms/uid-registry.md).

## 4 Implementation Model

### 4.1 Application Entities and Data Flow

LibreDCM has one Application Entity, called **LIBREDCM** here (the AE title is
configurable, `ae_title`). It listens on one TCP port (default 11112), either
plain or TLS. The same AE title is used when LibreDCM opens associations
(C-MOVE sub-operations, send, C-ECHO).

```
 Local real-world activities            Remote real-world activities

 Archive instance  <------ LIBREDCM AE <----- Remote AE sends images (C-STORE)
 Answer query      <------ LIBREDCM AE <----- Remote AE queries (C-FIND)
 Send instances    ------> LIBREDCM AE -----> Remote AE retrieves (C-MOVE / C-GET):
                                              C-STORE to the destination
 Verify            <-----> LIBREDCM AE <----> Remote AE verifies (C-ECHO, both ways)
 Administrator     ------> control API -----> LIBREDCM AE: C-ECHO test, send study
```

#### 4.1.1 Functional Definition of LIBREDCM

- **Verification SCP.** Answers C-ECHO with Success on any association.
- **Storage SCP.** Receives C-STORE. Checks the patient association against
  the index, writes the data set exactly as received into a Part 10 file,
  makes it durable, records it in the index and only then answers Success
  (DCM-SRS-002). Publishes an "instance stored" event for other Libre RWS
  services.
- **Query SCP.** Answers C-FIND from the index (section 5.2.7.5).
- **Move SCP.** Finds the instances for a C-MOVE, opens associations to the
  configured destination and sends each instance as stored (section 5.2.7.7).
- **Get SCP.** Sends the matched instances back on the requestor's
  association (section 5.2.7.9).
- **Verification SCU.** `libredcm -echo` (install check) and the control API
  connection test.
- **Storage SCU.** Only for C-MOVE sub-operations and the control API send.

LibreDCM keeps no state between associations other than the archive, so
nodes can be added and removed behind a load balancer.

## 5 Service and Interoperability Description

### 5.1 Mapping of Services to Application Entities

| Service | SOP Class / role | AE |
|---|---|---|
| Verification | SCP; SCU | LIBREDCM |
| Storage | SCP (receive); SCU (send for C-MOVE, C-GET, control API send) | LIBREDCM |
| Query/Retrieve | SCP of Patient Root and Study Root FIND, MOVE, GET | LIBREDCM |

### 5.2 DIMSE Services

#### 5.2.1 Basic Worklist Management Service - N/A

N/A.

#### 5.2.2 Modality Performed Procedure Step Service - N/A

N/A.

#### 5.2.3 Unified Worklist and Procedure Step Service - N/A

N/A.

#### 5.2.4 Instance Availability Notification Service - N/A

N/A.

#### 5.2.5 Storage Service

##### 5.2.5.1 SCU of the Storage SOP Classes

LibreDCM sends stored instances only for C-MOVE, C-GET and the control API
send. For a C-MOVE or a send it groups the instances by stored transfer
syntax and opens one association per group (and per 120 SOP classes), one
group after the other. For a group stored in Implicit or Explicit VR Little
Endian it proposes both little endian syntaxes (Explicit first). For a group
stored compressed it proposes only that compressed syntax (evidence 32, 33).

###### 5.2.5.1.1 Transcoding of Transfer Syntaxes

LibreDCM never transcodes compressed pixel data. If the destination does not
accept the stored compressed syntax, those sub-operations fail (evidence 33);
when it does, the data set is sent as stored (evidence 34: the seven
compressed objects arrived). Between the two native little endian syntaxes
LibreDCM re-encodes the data set when the destination accepted only the other
one; pixel values stay identical (DCM-SRS-010, `TestPixelDataIntegrity`).
C-GET sends only natively stored instances, encoded in the syntax the
requestor accepted.

##### 5.2.5.2 SCP of the Storage SOP Classes

As a Service Class Provider of the Storage Service Class, LibreDCM receives
C-STORE-RQ messages from remote SCUs. See Table 1-1 for the supported SOP
Classes.

**Table 5-1. Levels of Conformance**

| | |
|---|---|
| Level of Conformance | 2 (Full). All Type 1, 2 and 3 attributes and all private attributes are stored; the data set is archived as received |
| Level of Digital Signature Support | Signatures are not checked or created. They stay intact because the data set is not changed, except when Patient ID coercion (Table 5-2) changes an instance |

LibreDCM coerces the attributes in Table 5-2 only when configured to.

**Table 5-2. Attribute Coercion by Storage SCP**

| Attribute Name | Tag | SOP Class UID | Type of Change | New Value | Condition | Comments |
|---|---|---|---|---|---|---|
| Patient ID | (0010,0020) | ALL | MODIFIED | `B000_` + received Patient ID | CONFIGURATION | Only with `patient_conflict_coerce_patient_id: true`, for an instance whose Study Instance UID is already archived under another Patient ID; answered with Warning B000. Not done when the new value would exceed 64 characters (evidence 21, 71) |
| Original Attributes Sequence | (0400,0561) | ALL | ADDED | One item: Modified Attributes Sequence with the received Patient ID, Attribute Modification DateTime, Modifying System `LibreDCM`, Reason for the Attribute Modification `COERCE` | CONFIGURATION | Same condition as above (evidence 71) |

LibreDCM does not display or process instances, so there are no restrictions
on display or post-processing to state.

**Table 5-3. Behavior when storing Instances**

| Action upon Receiving | Condition | System Behavior |
|---|---|---|
| Store | Success | Data set written unchanged into a Part 10 file (File Meta with LibreDCM's Implementation Class UID and Version Name), made durable, indexed; then Success 0000 (evidence 14, 15, 70) |
| Check identifiers | SOP Instance UID in the data set differs from the command, or Study or Series Instance UID missing | Nothing stored; A900 (evidence 17) |
| Check data set | No data set, or it cannot be decoded | Nothing stored; C000 |
| Duplicate Instance | Same SOP Instance UID again, same patient and study | Overwrite: the new file replaces the old one and the index entry is updated; Success (evidence 18). DCM-SRS-023 (Draft) |
| Add to an existing study | Study Instance UID archived under another Patient ID; or Series Instance UID under another study; or SOP Instance UID under another series or study | Policy `quarantine` (default): stored under its own key, held, hidden from query and retrieve; Success (evidence 19, 30). `reject`: nothing stored; A778 (evidence 20). `accept_separate`: stored and returned as a separate patient/study entry; Success. With coercion on, a Patient ID conflict gets Warning B000 (Table 5-2). Every case writes an audit record and a warning log line (DCM-SRS-070…076) |
| Add to an existing study | Same Patient ID, different patient name, birth date or sex | Stored; the first-registered demographics are kept in the index; warning log line and metric (DCM-SRS-076) |
| Archive failure | Storage or index write fails, or a File Meta value cannot be encoded | A700; no Success is sent for an instance that was not stored (DCM-SRS-002, -018) |

**Table 5-4. Image Compression by Storage SCP**

| SOP Class | Behavior | Transfer Syntax | Comments |
|---|---|---|---|
| All SOP Classes | AS_IS | The negotiated transfer syntax | Stored as received |

Additional SOP classes are supported only with `accept_any_sop_class: true`:
LibreDCM then accepts any proposed abstract syntax in any transfer syntax of
Table 1-2 and stores the objects. Objects are kept until an administrator
removes them; LibreDCM has no automatic deletion. Any peer allowed to
associate can query and retrieve them.

#### 5.2.6 Storage Commitment Service - N/A

N/A.

#### 5.2.7 Query/Retrieve Service Class

##### 5.2.7.1 SCU of the Study Root Q/R Information Model - FIND SOP Class - N/A

N/A.

##### 5.2.7.2 SCU of the Patient Root Q/R Information Model - FIND SOP Class - N/A

N/A.

##### 5.2.7.3 SCU of the Study Root Q/R Information Model - MOVE SOP Class - N/A

N/A.

##### 5.2.7.4 SCU of the Patient Root Q/R Information Model - MOVE SOP Class - N/A

N/A.

##### 5.2.7.5 SCP of the Study Root Q/R Information Model - FIND SOP Class

As a Service Class Provider of the Study Root Q/R Information Model - FIND SOP
Class, LibreDCM uses the C-FIND-RSP to send matches back to the SCU. It
supports the keys in Tables 5-5 to 5-7 for hierarchical queries at the STUDY,
SERIES and IMAGE levels (evidence 23 to 28). A query at PATIENT level in
Study Root is answered with A900 (evidence 29).

Matching types: SINGLE_VALUE, UID (list of UIDs), WILDCARD (`*` and `?`),
RANGE (dates and times, open or closed) and UNIVERSAL (the attribute is
returned). A key without UNIVERSAL can be used for matching at that level but
comes back zero-length. Any other attribute in the identifier, including
private attributes and sequences, is returned zero-length and not used for
matching. Command (0000,xxxx) and File Meta (0002,xxxx) elements in an
identifier are ignored.

**Table 5-5. C-FIND keys, STUDY level** (generated)

<!-- dcs:begin query-keys-study -->
| Attribute Name | Tag | Keyword | Matching Type | Comments |
|---|---|---|---|---|
| Study Date | (0008,0020) | `StudyDate` | SINGLE_VALUE; RANGE; UNIVERSAL |  |
| Study Time | (0008,0030) | `StudyTime` | SINGLE_VALUE; RANGE; UNIVERSAL | A partial time covers its whole period |
| Accession Number | (0008,0050) | `AccessionNumber` | SINGLE_VALUE; WILDCARD; UNIVERSAL | Case-sensitive |
| Modalities in Study | (0008,0061) | `ModalitiesInStudy` | SINGLE_VALUE; UNIVERSAL | Matches a study with at least one series of any listed modality |
| Referring Physician's Name | (0008,0090) | `ReferringPhysicianName` | SINGLE_VALUE; WILDCARD; UNIVERSAL | Case-sensitive |
| Study Description | (0008,1030) | `StudyDescription` | SINGLE_VALUE; WILDCARD; UNIVERSAL | Case-sensitive |
| Patient's Name | (0010,0010) | `PatientName` | SINGLE_VALUE; WILDCARD; UNIVERSAL | Case-insensitive unless `find_case_sensitive_names` is set |
| Patient ID | (0010,0020) | `PatientID` | SINGLE_VALUE; WILDCARD; UNIVERSAL | Case-sensitive |
| Patient's Birth Date | (0010,0030) | `PatientBirthDate` | SINGLE_VALUE; RANGE; UNIVERSAL |  |
| Patient's Sex | (0010,0040) | `PatientSex` | SINGLE_VALUE; UNIVERSAL |  |
| Study Instance UID | (0020,000D) | `StudyInstanceUID` | SINGLE_VALUE; UID; UNIVERSAL |  |
| Study ID | (0020,0010) | `StudyID` | SINGLE_VALUE; WILDCARD; UNIVERSAL | Case-sensitive |
| Number of Study Related Series | (0020,1206) | `NumberOfStudyRelatedSeries` | UNIVERSAL | Return key, computed by LibreDCM |
| Number of Study Related Instances | (0020,1208) | `NumberOfStudyRelatedInstances` | UNIVERSAL | Return key, computed by LibreDCM |
<!-- dcs:end query-keys-study -->

**Table 5-6. C-FIND keys, SERIES level** (generated)

<!-- dcs:begin query-keys-series -->
| Attribute Name | Tag | Keyword | Matching Type | Comments |
|---|---|---|---|---|
| Study Date | (0008,0020) | `StudyDate` | SINGLE_VALUE; RANGE | matching only: returned zero-length at this level |
| Study Time | (0008,0030) | `StudyTime` | SINGLE_VALUE; RANGE | A partial time covers its whole period; matching only: returned zero-length at this level |
| Accession Number | (0008,0050) | `AccessionNumber` | SINGLE_VALUE; WILDCARD | Case-sensitive; matching only: returned zero-length at this level |
| Modality | (0008,0060) | `Modality` | SINGLE_VALUE; UNIVERSAL |  |
| Referring Physician's Name | (0008,0090) | `ReferringPhysicianName` | SINGLE_VALUE; WILDCARD | Case-sensitive; matching only: returned zero-length at this level |
| Study Description | (0008,1030) | `StudyDescription` | SINGLE_VALUE; WILDCARD | Case-sensitive; matching only: returned zero-length at this level |
| Series Description | (0008,103E) | `SeriesDescription` | SINGLE_VALUE; WILDCARD; UNIVERSAL | Case-sensitive |
| Patient's Name | (0010,0010) | `PatientName` | SINGLE_VALUE; WILDCARD | Case-insensitive unless `find_case_sensitive_names` is set; matching only: returned zero-length at this level |
| Patient ID | (0010,0020) | `PatientID` | SINGLE_VALUE; WILDCARD; UNIVERSAL | Case-sensitive |
| Patient's Birth Date | (0010,0030) | `PatientBirthDate` | SINGLE_VALUE; RANGE | matching only: returned zero-length at this level |
| Patient's Sex | (0010,0040) | `PatientSex` | SINGLE_VALUE | matching only: returned zero-length at this level |
| Study Instance UID | (0020,000D) | `StudyInstanceUID` | SINGLE_VALUE; UID; UNIVERSAL |  |
| Series Instance UID | (0020,000E) | `SeriesInstanceUID` | SINGLE_VALUE; UID; UNIVERSAL |  |
| Study ID | (0020,0010) | `StudyID` | SINGLE_VALUE; WILDCARD | Case-sensitive; matching only: returned zero-length at this level |
| Series Number | (0020,0011) | `SeriesNumber` | SINGLE_VALUE; UNIVERSAL |  |
| Number of Series Related Instances | (0020,1209) | `NumberOfSeriesRelatedInstances` | UNIVERSAL | Return key, computed by LibreDCM |
<!-- dcs:end query-keys-series -->

**Table 5-7. C-FIND keys, IMAGE level** (generated)

<!-- dcs:begin query-keys-image -->
| Attribute Name | Tag | Keyword | Matching Type | Comments |
|---|---|---|---|---|
| SOP Class UID | (0008,0016) | `SOPClassUID` | SINGLE_VALUE; UID; UNIVERSAL |  |
| SOP Instance UID | (0008,0018) | `SOPInstanceUID` | SINGLE_VALUE; UID; UNIVERSAL |  |
| Study Date | (0008,0020) | `StudyDate` | SINGLE_VALUE; RANGE | matching only: returned zero-length at this level |
| Study Time | (0008,0030) | `StudyTime` | SINGLE_VALUE; RANGE | A partial time covers its whole period; matching only: returned zero-length at this level |
| Accession Number | (0008,0050) | `AccessionNumber` | SINGLE_VALUE; WILDCARD | Case-sensitive; matching only: returned zero-length at this level |
| Modality | (0008,0060) | `Modality` | SINGLE_VALUE; UNIVERSAL |  |
| Referring Physician's Name | (0008,0090) | `ReferringPhysicianName` | SINGLE_VALUE; WILDCARD | Case-sensitive; matching only: returned zero-length at this level |
| Study Description | (0008,1030) | `StudyDescription` | SINGLE_VALUE; WILDCARD | Case-sensitive; matching only: returned zero-length at this level |
| Series Description | (0008,103E) | `SeriesDescription` | SINGLE_VALUE; WILDCARD | Case-sensitive; matching only: returned zero-length at this level |
| Patient's Name | (0010,0010) | `PatientName` | SINGLE_VALUE; WILDCARD | Case-insensitive unless `find_case_sensitive_names` is set; matching only: returned zero-length at this level |
| Patient ID | (0010,0020) | `PatientID` | SINGLE_VALUE; WILDCARD; UNIVERSAL | Case-sensitive |
| Patient's Birth Date | (0010,0030) | `PatientBirthDate` | SINGLE_VALUE; RANGE | matching only: returned zero-length at this level |
| Patient's Sex | (0010,0040) | `PatientSex` | SINGLE_VALUE | matching only: returned zero-length at this level |
| Study Instance UID | (0020,000D) | `StudyInstanceUID` | SINGLE_VALUE; UID; UNIVERSAL |  |
| Series Instance UID | (0020,000E) | `SeriesInstanceUID` | SINGLE_VALUE; UID; UNIVERSAL |  |
| Study ID | (0020,0010) | `StudyID` | SINGLE_VALUE; WILDCARD | Case-sensitive; matching only: returned zero-length at this level |
| Series Number | (0020,0011) | `SeriesNumber` | SINGLE_VALUE | matching only: returned zero-length at this level |
| Instance Number | (0020,0013) | `InstanceNumber` | SINGLE_VALUE; UNIVERSAL |  |
<!-- dcs:end query-keys-image -->

- Extended negotiation for relational queries, date-time matching, fuzzy
  semantic matching of person names and timezone adjustment is not supported
  (not answered; evidence 60).
- Higher-level unique keys are not required (deviation 7 in 3.3.1). Only the
  first value of a multi-valued key is used, except for UID lists and
  Modalities in Study (deviation 8).
- Wildcard and single-value matching of text is case-sensitive, except for
  Patient's Name, which is case-insensitive (also for non-ASCII letters) unless
  `find_case_sensitive_names` is set (evidence 23, 26). A lone `*` or an empty
  value is universal matching. Keys marked SINGLE_VALUE without WILDCARD (for
  example Modality, Patient's Sex) are compared literally.
- Dates (DA) match a single date or a range; times (TM) match a single time or
  a range, where a partial time covers its whole period (`0830` matches 08:30:00
  to 08:30:59.999999). ACR-NEMA style `08:30` is accepted. Combined date-time
  matching is not supported (evidence 24).
- Matches are sorted: patients by Patient ID, studies by Study Date (newest
  first), series by Series Number, images by series and Instance Number. At
  most `find_limit` matches are returned (deviation 5).
- Held conflict instances are not returned (default `hidden`); with
  `patient_conflict_visibility: flagged`, and always for accepted conflicts,
  they are returned as a separate entry under their own Patient ID
  (DCM-SRS-073; evidence 30).
- A C-CANCEL-RQ is acted on between responses: LibreDCM stops and sends
  Cancel (FE00).
- Character sets: section 5.7.

##### 5.2.7.6 SCP of the Patient Root Q/R Information Model - FIND SOP Class

As 5.2.7.5, with the PATIENT level added (Table 5-8). The STUDY, SERIES and
IMAGE levels use Tables 5-5 to 5-7 (evidence 22).

**Table 5-8. C-FIND keys, PATIENT level** (generated)

<!-- dcs:begin query-keys-patient -->
| Attribute Name | Tag | Keyword | Matching Type | Comments |
|---|---|---|---|---|
| Patient's Name | (0010,0010) | `PatientName` | SINGLE_VALUE; WILDCARD; UNIVERSAL | Case-insensitive unless `find_case_sensitive_names` is set |
| Patient ID | (0010,0020) | `PatientID` | SINGLE_VALUE; WILDCARD; UNIVERSAL | Case-sensitive |
| Patient's Birth Date | (0010,0030) | `PatientBirthDate` | SINGLE_VALUE; RANGE; UNIVERSAL |  |
| Patient's Sex | (0010,0040) | `PatientSex` | SINGLE_VALUE; UNIVERSAL |  |
| Number of Patient Related Studies | (0020,1200) | `NumberOfPatientRelatedStudies` | UNIVERSAL | Return key, computed by LibreDCM |
<!-- dcs:end query-keys-patient -->

##### 5.2.7.7 SCP of the Study Root Q/R Information Model - MOVE SOP Class

- LibreDCM identifies the instances from the identifier with the same
  matching as C-FIND, at the image level. At least one of Patient ID, Study
  Instance UID, Series Instance UID or SOP Instance UID (a list of UIDs is
  allowed) must be present with a value. An identifier without any of them
  (for example only an empty Study Instance UID) is refused with A900, so that
  a mistaken request cannot move the whole archive (evidence 36).
- The Move Destination must be a configured remote AE (`remote_aes`, section
  6.2.7). An unknown destination is refused with A801 and nothing is sent
  (DCM-SRS-011, evidence 35).
- LibreDCM opens new associations to the destination for the C-STORE
  sub-operations (sections 5.2.5.1, 7.2.1.3.1). Calling AE title: LibreDCM's
  AE title; called AE title: the destination's `ae_title` (default: its name).
  The C-STORE-RQ carries no Move Originator AE Title or Message ID (optional
  in PS3.7; evidence 32).
- Held conflict instances are sent only when the request names their Patient
  ID and they are visible (DCM-SRS-073).
- No Pending responses are sent. C-CANCEL is not acted on (deviation 4).
- Final response: Success when all sub-operations completed; B000 when some
  failed or had a warning; A702 when all failed (evidence 32, 33, 34). The
  final response carries the Failed SOP Instance UID List when there were
  failures. A move that matches nothing ends with Success and zero
  sub-operations.

##### 5.2.7.8 SCP of the Patient Root Q/R Information Model - MOVE SOP Class

As 5.2.7.7, with the PATIENT level (evidence 37).

##### 5.2.7.9 SCP of the Study Root and Patient Root Q/R Information Models - GET SOP Classes

(Added at the end of 5.2.7; C-GET is not in the PS3.2 template.)

- The instances are identified as for C-MOVE (a unique key is required, A900
  otherwise).
- LibreDCM sends the C-STORE sub-operations on the same association. It
  accepts SCP/SCU role selection for every Storage SOP Class of Table 1-1 (it
  agrees to be the SCU of the storage classes; evidence 38: the SCP role was
  accepted for all 106 storage classes of DCMTK `getscu`'s proposal that are
  in Table 1-1; the 14 retired classes it also proposes were refused). It
  sends each instance on an accepted presentation context for its SOP class,
  in the syntax the requestor accepted.
- Only instances stored in Implicit or Explicit VR Little Endian are sent;
  instances stored compressed are failed sub-operations (deviation 3; evidence
  39). The total size is capped by `get_max_bytes`.
- A missing storage context for a matched SOP class ends the association
  (deviation 1). Completed counts are not checked against the requestor's
  C-STORE responses (deviation 2).
- Final response statuses as for C-MOVE (Success, B000, A702; evidence 38,
  39); A900 for a bad identifier; A701 when the index cannot be read.

#### 5.2.8 Print Management Service - N/A

N/A.

### 5.3 DICOM Web Services - N/A

N/A.

### 5.4 Media Services - N/A

N/A.

### 5.5 Real-Time Video Services - N/A

N/A.

### 5.6 Cross Service Considerations

- C-FIND, C-MOVE, C-GET and the control API study search use one matching
  function and the same patient-conflict visibility rules, so a study found by
  C-FIND is the study that C-MOVE sends (DCM-SRS-091).
- An instance is visible to C-FIND only after it is durable in storage and in
  the index, and only then does the sender get Success.
- In a cluster every node reads the same index and storage, so any node
  answers for instances stored by any other node.

### 5.7 Specific Character Sets

- **Storage.** The data set is stored with its own Specific Character Set
  (0008,0005), unchanged. LibreDCM decodes the text attributes it indexes
  (Table 9-1) with the godicom character-set decoder (PS3.5 chapter 6,
  including ISO 2022 code extensions). Verified for this statement: ISO_IR 100
  (`TestFindRefinementsEndToEnd`) and ISO_IR 192 (evidence 22, 26). Other
  character sets are decoded by the same library but were not tested here.
- **C-FIND requests.** The identifier is decoded with its Specific Character
  Set. Matching is done on Unicode text, so a request in one character set
  finds a value received in another.
- **C-FIND responses.** Values come from the index as Unicode and are written
  as UTF-8. When any returned value is not ASCII the response carries
  Specific Character Set `ISO_IR 192` (PS3.4 C.4.1.1.3.2 allows this whatever
  the request used); otherwise it carries the request's value (DCM-SRS-009;
  evidence 22, 26). A requestor that cannot read UTF-8 sees non-ASCII names
  garbled; re-encoding into the request's character set is not done.
- **Retrieve.** C-MOVE and C-GET send the stored data set, so its Specific
  Character Set is unchanged.

## 6 Configuration

### 6.1 General Configuration Parameters

Configuration comes from a YAML file (`-config`) with environment-variable
overrides; an environment variable that is set but empty does not override the
file (DCM-SRS-050). Legacy `RWS_DICOM_*` names are still read. The allow-list
and the remote AEs can be changed without a restart (SIGHUP, or the control
API through Libre WT); open associations are not affected (DCM-SRS-016).
Every other setting needs a restart.

**Table 6-1. DICOM listener settings** (generated)

<!-- dcs:begin configuration -->
| Setting (`dicom:`) | Environment variable | Default |
|---|---|---|
| `ae_title` | `LIBRE_DCM_AE_TITLE` | `LIBREDCM` |
| `listen` | `LIBRE_DCM_LISTEN` | `:11112` |
| `allow_any_called_ae_title` | `LIBRE_DCM_ALLOW_ANY_CALLED_AE` | `false` |
| `accept_any_sop_class` | `LIBRE_DCM_ACCEPT_ANY_SOP_CLASS` | `false` |
| `max_associations` | `LIBRE_DCM_MAX_ASSOCIATIONS` | `64` |
| `max_pdu_length` | `LIBRE_DCM_MAX_PDU` | `131072` |
| `handshake_timeout` | `LIBRE_DCM_HANDSHAKE_TIMEOUT` | `30s` |
| `idle_timeout` | `LIBRE_DCM_IDLE_TIMEOUT` | `5m0s` |
| `find_limit` | `LIBRE_DCM_FIND_LIMIT` | `1000` |
| `get_max_bytes` | `LIBRE_DCM_GET_MAX_BYTES` | `1073741824` |
| `tls_cert_file` | `LIBRE_DCM_TLS_CERT` | (empty) |
| `tls_key_file` | `LIBRE_DCM_TLS_KEY` | (empty) |
| `remote_aes` | `LIBRE_DCM_REMOTE_AES` (`NAME=HOST:PORT[:AE]`, comma-separated) | (none) |
| `allowed_aes` | `LIBRE_DCM_ALLOWED_AES` | (empty) |
| `find_case_sensitive_names` | `LIBRE_DCM_FIND_CASE_SENSITIVE_NAMES` | `false` |
| `audit_log_off` | `LIBRE_DCM_AUDIT_LOG_OFF` | `false` |
| `events_omit_patient_id` | `LIBRE_DCM_EVENTS_OMIT_PATIENT_ID` | `false` |
| `audit_omit_patient_id` | `LIBRE_DCM_AUDIT_OMIT_PATIENT_ID` | `false` |
| `patient_conflict_policy` | `LIBRE_DCM_PATIENT_CONFLICT_POLICY` | `quarantine` |
| `patient_conflict_visibility` | `LIBRE_DCM_PATIENT_CONFLICT_VISIBILITY` | `hidden` |
| `patient_conflict_coerce_patient_id` | `LIBRE_DCM_PATIENT_CONFLICT_COERCE_PATIENT_ID` | `false` |
<!-- dcs:end configuration -->

Meaning of the settings:

- `ae_title`: LibreDCM's AE title, as called AE (acceptor) and calling AE
  (initiator). Calls to any other AE title are rejected unless
  `allow_any_called_ae_title` is true.
- `listen`: TCP address and port of the DICOM listener.
- `accept_any_sop_class`: accept any proposed abstract syntax (section
  5.2.5.2).
- `max_associations`: associations served at once per node; more get a
  transient A-ASSOCIATE-RJ.
- `max_pdu_length`, `handshake_timeout`, `idle_timeout`: Table 7-1.
- `find_limit`: most C-FIND matches returned (deviation 5).
- `get_max_bytes`: most bytes one C-GET may send.
- `tls_cert_file`, `tls_key_file`: PEM certificate and key; when set, the
  listener accepts only TLS (section 8.4.2).
- `remote_aes`: C-MOVE destinations (section 6.2.7).
- `allowed_aes`: calling-AE allow-list (section 8.7.2).
- `find_case_sensitive_names`: turn off case-insensitive Patient's Name
  matching.
- `audit_log_off`, `audit_omit_patient_id`, `events_omit_patient_id`: audit
  and event content (section 11.2.2).
- `patient_conflict_policy`, `patient_conflict_visibility`,
  `patient_conflict_coerce_patient_id`: Tables 5-2 and 5-3. An unknown value
  falls back to the safest one (`quarantine`, `hidden`).

### 6.2 Configuration of DIMSE Services

#### 6.2.1 Basic Worklist Management Service Configuration - N/A

N/A.

#### 6.2.2 Modality Performed Procedure Step Service Configuration - N/A

N/A.

#### 6.2.3 Unified Worklist and Procedure Step Service Configuration - N/A

N/A.

#### 6.2.4 Instance Availability Notification Service Configuration - N/A

N/A.

#### 6.2.5 Storage Service Configuration

The storage backend (filesystem or S3) and the index (SQLite or PostgreSQL)
are chosen in the `storage:` and `index:` sections; they do not change the
DICOM behaviour. See `modules/dicom/config.example.yaml` and INSTALL.md.

#### 6.2.6 Storage Commitment Service Configuration - N/A

N/A.

#### 6.2.7 Query/Retrieve Service Configuration

C-MOVE destinations (AE title to presentation address mapping of remote AEs):

```yaml
dicom:
  remote_aes:
    VIEWER1: {host: 10.0.0.20, port: 11112}            # called AE title VIEWER1
    PACS_B:  {host: pacs-b.example, port: 104, ae_title: PACSB}
```

or `LIBRE_DCM_REMOTE_AES="VIEWER1=10.0.0.20:11112,PACS_B=pacs-b.example:104:PACSB"`.
The key is the AE title a C-MOVE request names as Move Destination. Invalid
entries are reported and not used (DCM-SRS-013). The local AE title and
presentation address are `ae_title` and `listen` (Table 6-1).

#### 6.2.8 Print Management Service Configuration - N/A

N/A.

### 6.3 Configuration of DICOM Web Services - N/A

N/A.

### 6.4 Configuration of Media Storage Service - N/A

N/A.

### 6.5 Configuration of Real-Time Video Service - N/A

N/A.

### 6.6 Configuration of Audit Trail - Syslog - N/A

N/A. Audit records are structured log lines (section 11.2.2), not syslog
messages.

## 7 Network and Media Communication Details

### 7.1 General

One AE, LIBREDCM, accepts associations for C-ECHO, C-STORE, C-FIND, C-MOVE
and C-GET, and opens associations for C-MOVE sub-operations, the control API
send and C-ECHO.

#### 7.1.1 General Association Parameters

**Table 7-1. General Association Parameters** (generated)

<!-- dcs:begin association-parameters -->
| Name | Value |
|---|---|
| Application Context Name | `1.2.840.10008.3.1.1.1` |
| Implementation Class UID | `1.3.6.1.4.1.67686.1.1.1.<major>.<minor>` (version 0.2.x: `1.3.6.1.4.1.67686.1.1.1.0.2`) |
| Implementation Version Name | `LIBREDCM_<major>_<minor>` (version 0.2.x: `LIBREDCM_0_2`) |
| Maximum PDU length received | Default: 131072 bytes (`max_pdu_length`) |
| Association negotiation timeout (acceptor) | Default: 30s (`handshake_timeout`) |
| Idle timeout per PDU read or write | Default: 5m0s (`idle_timeout`) |
| Maximum number of simultaneous associations as acceptor | Default: 64 per node (`max_associations`) |
| Maximum number of simultaneous associations as initiator | One per running C-MOVE or control-API send (not limited separately) |
| Maximum number of outstanding asynchronous operations | 1 (asynchronous operations window not negotiated) |
| File Meta Information Version | `00\01` |
| C-FIND result limit | Default: 1000 matches (`find_limit`) |
| C-GET buffer limit | Default: 1073741824 bytes (`get_max_bytes`) |
<!-- dcs:end association-parameters -->

The Implementation Class UID and Version Name follow the Libre Systems UID
registry and change with each minor version; a build without a release
version sends `1.3.6.1.4.1.67686.1.1.1.0.0` / `LIBREDCM_0_0` (DCM-SRS-098).
DCMTK saw `1.3.6.1.4.1.67686.1.1.1.0.2` / `LIBREDCM_0_2` in the
A-ASSOCIATE-AC and in LibreDCM's own A-ASSOCIATE-RQ, and the same
identification in all 11 stored files (evidence 10, 32, 70). LibreDCM never
sends PDUs larger than the peer's Maximum Length.

### 7.2 Specifications

#### 7.2.1 LIBREDCM Application Entity

##### 7.2.1.1 Sequencing of Real-World Activities for LIBREDCM

- **Store:** a remote SCU opens an association, sends one or more C-STORE-RQ
  and releases it. Each instance is answered after it is archived.
- **Query:** C-FIND-RQ, then one Pending response per match, then Success.
- **Move:** C-MOVE-RQ on the requestor's association. LibreDCM opens one or
  more new associations to the destination, sends the C-STORE sub-operations
  one at a time, releases them, and then sends the single final C-MOVE-RSP.
- **Get:** C-GET-RQ; LibreDCM sends the C-STORE-RQ sub-operations on the same
  association, one at a time, each waiting for its C-STORE-RSP, then the
  final C-GET-RSP.
- Operations on one association are handled one after the other.

##### 7.2.1.2 Association Parameters of LIBREDCM - N/A

N/A. Table 7-1 applies.

##### 7.2.1.3 Association Initiation

###### 7.2.1.3.1 Real-World Activity: Send instances (C-MOVE sub-operations, control API send)

- Presentation contexts: one per SOP class in the group, with the stored
  compressed syntax, or Explicit then Implicit VR Little Endian for natively
  stored instances (evidence 32). At most 120 contexts per association.
- All instances of a group go over one association, one after the other. No
  parallel associations.
- Connection timeout 10 s. A failed association fails all sub-operations of
  that group (counted as failed, listed in the Failed SOP Instance UID List);
  there is no automatic retry. The association is released (aborted if the
  release fails) after the last instance.
- Calling AE title: `ae_title`. Called AE title: the destination's `ae_title`.

###### 7.2.1.3.2 Real-World Activity: Verification test

`libredcm -echo HOST:PORT` (called AE `-echo-ae`, TLS with `-echo-tls`) and
`POST /control/v1/echo` propose one presentation context: Verification with
Implicit VR Little Endian. Timeout 15 s. With TLS the peer certificate is not
verified: this is a reachability test and no patient data is sent.

**Table 7-2. Extended Negotiation - Association Initiation**

| SOP Class | Extended Negotiation | Support | Requested Value |
|---|---|---|---|
| All Storage SOP Classes (Table 1-1) | Level of support; Level of Digital Signature support; Element Coercion | N | Not sent |

LibreDCM sends no SCP/SCU role selection, asynchronous operations window,
SOP class extended or common extended negotiation, or user identity items
when it initiates an association.

##### 7.2.1.4 Association Acceptance

LibreDCM checks an A-ASSOCIATE-RQ in this order and rejects at the first
failure (section 7.3.1.2):

1. The protocol version must include version 1.
2. The Calling AE Title must not be empty.
3. When an allow-list is configured, the calling AE title (and the peer's IP
   address, for `AE@IP` and `AE@CIDR` entries) must be on it.
4. The Called AE Title must be `ae_title` (unless `allow_any_called_ae_title`).

A connection that arrives while `max_associations` associations are running
is rejected before these checks.

Then each proposed presentation context is accepted or rejected on its own:

- Abstract syntax: Verification, the Query/Retrieve classes of Table 1-3 and
  the Storage classes of Table 1-1 (any abstract syntax with
  `accept_any_sop_class`). Others: rejected, "abstract syntax not supported"
  (evidence 60).
- Transfer syntax for a Storage class: the **first** syntax in the proposer's
  list that is in Table 1-2, so compressed data is kept compressed when the
  sender prefers it (evidence 15). For Verification and Query/Retrieve:
  Explicit VR Little Endian if proposed, else Implicit VR Little Endian, else
  the first proposed syntax from Table 1-2. None acceptable: "transfer
  syntaxes not supported" (evidence 16, 60).
- SCP/SCU role selection: answered for the Storage classes of Table 1-1
  (C-GET); for other classes the default roles apply.
- The A-ASSOCIATE-AC carries Maximum Length 131072 (default) and the
  Implementation Class UID and Version Name.

**Table 7-3. Extended Negotiation - Association Acceptance**

| SOP Class | Extended Negotiation | Support | Requested Value |
|---|---|---|---|
| All Storage SOP Classes (Table 1-1) | Level of support; Level of Digital Signature support; Element Coercion | N | Not answered (defaults apply) |
| All Query/Retrieve FIND SOP Classes | Relational queries; Date-time matching; Fuzzy semantic matching of person names; Timezone query adjustment; Enhanced Multi-Frame Image Conversion | N | Not answered (evidence 60) |
| All Query/Retrieve MOVE and GET SOP Classes | Relational retrieval; Enhanced Multi-Frame Image Conversion | N | Not answered |

The asynchronous operations window is not answered, so one outstanding
operation in each direction applies (evidence 60: pynetdicom read the
acceptor window as 1/1). A user identity item is accepted and ignored; no
user identity response is sent (evidence 60).

###### 7.2.1.4.1 Real-World Activity: Verification

C-ECHO-RQ is answered with Success on any association (evidence 10, 12).

###### 7.2.1.4.2 Real-World Activity: Store instances

Section 5.2.5.2. One association can carry any number of C-STORE-RQ
(evidence 14: all 128 storage contexts of DCMTK `storescu`'s default proposal
accepted).

###### 7.2.1.4.3 Real-World Activity: Answer queries

Sections 5.2.7.5, 5.2.7.6.

###### 7.2.1.4.4 Real-World Activity: Retrieve (C-MOVE, C-GET)

Sections 5.2.7.7 to 5.2.7.9.

### 7.3 Status Codes

#### 7.3.1 General AE Communication and Failure Behavior and Handling

##### 7.3.1.1 Communication Failure Behavior as Association Initiator

**Table 7-4. DICOM Communication Failure Behavior as Association Initiator**

| Failure | Failure Behavior |
|---|---|
| Timeout (connect 10 s; then `idle_timeout` per PDU) | The sub-operations not yet done fail; the association is aborted; logged. Counted in the final C-MOVE response. No retry |
| Association rejected, or no presentation context accepted | All sub-operations of that group fail (evidence 33); logged |
| Association aborted or network disconnect | The current and remaining sub-operations of that group fail; logged |

##### 7.3.1.2 Communication Failure Handling as Association Acceptor

**Table 7-5. DICOM Communication Failure Handling as Association Acceptor**

| Exception | Failure response |
|---|---|
| Protocol version without version 1 | A-ASSOCIATE-RJ: rejected-permanent, service-provider (ACSE), protocol-version-not-supported |
| Empty Calling AE Title | A-ASSOCIATE-RJ: rejected-permanent, service-user, calling-AE-title-not-recognized |
| Calling AE not on the allow-list | A-ASSOCIATE-RJ: rejected-permanent, service-user, calling-AE-title-not-recognized; audit record `SecurityAlert.association-rejected` (evidence 13, 80) |
| Unrecognized Called AE Title | A-ASSOCIATE-RJ: rejected-permanent, service-user, called-AE-title-not-recognized; warning log line (evidence 11; deviation 6) |
| `max_associations` reached | A-ASSOCIATE-RJ: rejected-transient, service-provider (presentation), local-limit-exceeded |
| A-ASSOCIATE-RQ not completed within `handshake_timeout` | Connection closed |
| No PDU read or written for `idle_timeout` | Connection closed |
| Failure while processing a DIMSE message (malformed command or identifier, C-GET store without a context) | Connection closed (deviation 1) |
| Plain DICOM to a TLS listener | TLS handshake fails; connection closed (evidence 41) |

#### 7.3.2 DIMSE Services

##### 7.3.2.5 Storage Service

###### 7.3.2.5.1 SCU of the Storage SOP Classes - C-STORE

| Status received | Behavior |
|---|---|
| Success 0000 | Sub-operation counted as completed |
| Warning (0001, Bxxx) | Counted as warning |
| Any other status, or no response | Counted as failed; SOP Instance UID added to the Failed SOP Instance UID List; logged |

###### 7.3.2.5.2 SCP of the Storage SOP Classes - C-STORE

| Service Status | Further Meaning | Status Code | Condition |
|---|---|---|---|
| Success | Success | 0000 | Archived and indexed; also a held conflict instance under `quarantine` or `accept_separate` (evidence 14, 15, 18, 19) |
| Warning | Coercion of Data Elements | B000 | Patient ID coerced (Table 5-2; evidence 21) |
| Failure | Refused: Out of Resources | A700 | Storage or index failure; File Meta value cannot be encoded |
| Failure | Refused: conflicting Patient ID | A778 | Patient conflict under `patient_conflict_policy: reject` (in the A7xx "Refused: Out of Resources" range; evidence 20) |
| Failure | Error: Data Set does not match SOP Class | A900 | Study or Series Instance UID missing, or SOP Instance UID differs from the command (evidence 17) |
| Failure | Error: Cannot understand | C000 | No data set, or the data set cannot be decoded |
| Failure | Refused: SOP Class not supported | 0122 | The C-STORE-RQ names a SOP class other than the one of its presentation context |

##### 7.3.2.7 Query/Retrieve Service

###### 7.3.2.7.3 SCP of the Query/Retrieve FIND SOP Classes - C-FIND

| Service Status | Further Meaning | Status Code | Condition |
|---|---|---|---|
| Pending | Matches are continuing | FF00 | One per match |
| Success | Matching is complete | 0000 | After the last match (also when the result was cut at `find_limit`, deviation 5) |
| Cancel | Matching terminated due to Cancel | FE00 | C-CANCEL-RQ received between matches |
| Failure | Identifier does not match SOP Class | A900 | Missing or unknown Query/Retrieve Level, or PATIENT level in Study Root (evidence 29) |
| Failure | Unable to process | C000 | The index query failed |
| Failure | Refused: SOP Class not supported | 0122 | The request names another SOP class than its presentation context |

###### 7.3.2.7.4 SCP of the Query/Retrieve MOVE SOP Classes - C-MOVE

| Service Status | Further Meaning | Status Code | Condition |
|---|---|---|---|
| Success | Sub-operations complete, no failures | 0000 | All sub-operations completed, or no match (evidence 32, 34, 37) |
| Warning | Sub-operations complete, one or more failures or warnings | B000 | Some failed or had a warning; Failed SOP Instance UID List included |
| Failure | Refused: Out of Resources - Unable to perform sub-operations | A702 | All sub-operations failed (evidence 33) |
| Failure | Refused: Move Destination unknown | A801 | Destination not in `remote_aes` (evidence 35) |
| Failure | Identifier does not match SOP Class | A900 | Bad level, or no unique key (evidence 36) |
| Failure | Refused: Out of Resources - Unable to calculate number of matches | A701 | The index query failed |
| Failure | Refused: SOP Class not supported | 0122 | The request names another SOP class than its presentation context |

###### 7.3.2.7.5 SCP of the Query/Retrieve GET SOP Classes - C-GET

(Added; C-GET is not in the template.) Same codes as C-MOVE except A801. A702
is also sent, with no sub-operations, when the instances exceed
`get_max_bytes` (evidence 38, 39).

## 8 Security

### 8.1 Introduction

LibreDCM protects DICOM traffic with an optional TLS listener, rejects
unknown called AE titles, can limit which peers may associate, and writes
audit records. It does not authenticate users on the DICOM network. The
threat model is in [cybersecurity.md](cybersecurity.md).

### 8.2 External Network Requirements

- Run LibreDCM on a network that only trusted systems can reach, or use TLS
  and the allow-list.
- Keep the system clock synchronized (NTP): audit records and stored-file
  times use it.
- Protect the HTTP admin endpoint (default port 8081) with its admin token and
  HTTPS, and do not expose it to untrusted networks.

### 8.3 TCP Port Configuration

| Port | Use | Default |
|---|---|---|
| `listen` | DICOM, plain **or** TLS (TLS when a certificate is configured; 2762 is the registered port for DICOM over TLS). One listener per node | 11112 |
| `admin.addr` | HTTP or HTTPS: health, readiness, metrics, version, control API. Not DICOM | 8081 |

LibreDCM opens outgoing connections only to configured C-MOVE destinations and
to C-ECHO targets named by an administrator.

### 8.4 DICOM Security Profiles Support

#### 8.4.1 Secure Use and User Identity Profiles - N/A

N/A.

#### 8.4.2 Secure Transport Connection Profiles

When `tls_cert_file` and `tls_key_file` are set the DICOM listener accepts
only TLS, version 1.2 or later (DCM-SRS-043). TLS 1.1 is refused; TLS 1.2 and
1.3 are accepted (evidence 60). LibreDCM authenticates itself with its
certificate; it does **not** request or check client certificates (mutual
TLS, DCM-SRS-044, is Draft). Cipher suites are the Go standard library
defaults. DCMTK `echoscu` with certificate verification against LibreDCM's
certificate connected over TLS 1.3 (evidence 40). Because client
authentication is missing, LibreDCM does not claim a PS3.15 Secure Transport
Connection Profile in this version. Outgoing associations (C-MOVE
sub-operations) do not use TLS in this version.

#### 8.4.3 Media Storage Security Profiles - N/A

N/A.

#### 8.4.4 Attribute Confidentiality Profiles - N/A

N/A.

#### 8.4.5 Digital Signature Profiles - N/A

N/A. Signatures in stored objects are kept (Table 5-1) but not created or
checked.

#### 8.4.6 Additional DICOM Security Profiles - N/A

N/A. The audit records use the PS3.15 A.5 event names but are not sent as
Audit Trail Message Transmission Profile messages (section 11.2.2).

### 8.5 User Identity Negotiation Support

#### 8.5.1 Association Initiation

No user identity item is sent.

#### 8.5.2 Association Acceptance

A user identity item (any type) is ignored; the association is accepted and no
response item is sent, even when a positive response is requested (evidence
60).

### 8.6 Web Services Security Features - N/A

N/A.

### 8.7 Other Security Features

#### 8.7.1 Media Storage Security - N/A

N/A.

#### 8.7.2 Network Security

- **Called AE check.** Associations calling another AE title are rejected
  (DCM-SRS-014, evidence 11).
- **Calling-AE allow-list** (`allowed_aes`). Entries are `AE`, `AE@IP` or
  `AE@CIDR`. When the list is not empty, only listed peers may associate
  (evidence 12, 13). An invalid list rejects every peer instead of allowing
  all (DCM-SRS-015). Changes apply to new associations without a restart.
- AE titles are not authentication: any system that knows an allowed AE title
  and can reach the port from an allowed address can associate.

#### 8.7.3 Other Security Features

- Licensing never refuses, delays or interrupts a DICOM association or
  service (DCM-SRS-058).
- Event messages and audit records can leave the Patient ID out.

## 9 Information Object Definitions

LibreDCM creates no SOP Instances. It stores received instances unchanged,
except for the configurable Patient ID coercion (Table 5-2), which adds an
Original Attributes Sequence item.

### 9.1 Usage of Attributes from Received IODs

LibreDCM reads these attributes of each received instance into its index. They
are used for C-FIND matching and responses, retrieve and patient-conflict
checks. Indexing never changes the data set.

**Table 9-1. Attributes read from received instances**

| Attribute | Tag | Use |
|---|---|---|
| Patient ID | (0010,0020) | Index, patient-conflict check |
| Patient's Name, Birth Date, Sex | (0010,0010), (0010,0030), (0010,0040) | Index (first registration kept, DCM-SRS-076) |
| Study Instance UID | (0020,000D) | Required (A900 if missing); index; storage key |
| Study Date, Study Time, Accession Number, Study ID, Study Description, Referring Physician's Name | (0008,0020), (0008,0030), (0008,0050), (0020,0010), (0008,1030), (0008,0090) | Index |
| Series Instance UID | (0020,000E) | Required (A900 if missing); index |
| Modality, Series Number, Series Description | (0008,0060), (0020,0011), (0008,103E) | Index |
| SOP Instance UID, SOP Class UID | (0008,0018), (0008,0016) | Must agree with the C-STORE-RQ; index; storage key. Taken from the command when absent from the data set |
| Instance Number | (0020,0013) | Index |
| Specific Character Set | (0008,0005) | Decoding of the text attributes above |

### 9.2 Data Dictionary of Private Attributes

None. LibreDCM defines no private attributes. Private attributes of received
objects are stored unchanged.

### 9.3 Coded Terminology and Templates

None, except the Defined Term `COERCE` of Reason for the Attribute
Modification (0400,0565) in Table 5-2.

### 9.4 Grayscale Image Consistency - N/A

N/A. LibreDCM does not display images.

### 9.5 Standard Extended, Specialized and Private SOP Classes

None. With `accept_any_sop_class` LibreDCM can store objects of private SOP
classes it does not know (section 5.2.5.2); it defines none.

### 9.6 Private Transfer Syntaxes

None.

## 10 Structured Report Content Encoding - N/A

N/A.

## 11 Security Details

### 11.1 External Network Requirement Details

#### 11.1.1 Basic Time Synchronization

LibreDCM uses the operating system clock. It does not implement NTP; the host
must keep time (section 8.2).

#### 11.1.2 Basic Network Address Management - N/A

N/A.

#### 11.1.3 Application Configuration Management - N/A

N/A. LDAP configuration is not supported.

#### 11.1.4 DNS Service Discovery - N/A

N/A.

### 11.2 DICOM Security Profile Details

#### 11.2.1 Online Electronic Storage Secure Use - N/A

N/A.

#### 11.2.2 Audit Trail Messages

LibreDCM writes audit records as structured log lines with `audit=true`, named
after the PS3.15 A.5 audit message types (DCM-SRS-040). Each DICOM record
carries the outcome, calling and called AE titles and the peer address
(evidence 80). They are not PS3.15 XML audit messages and are not sent by
syslog (DCM-SRS-045, Draft). `audit_log_off` turns them off;
`audit_omit_patient_id` leaves Patient IDs out.

| Audit event | Trigger | Main content |
|---|---|---|
| `DICOMInstancesTransferred.received` | Instance stored | Study, Patient ID, SOP Instance UID |
| `Query` | C-FIND answered | Level, filters, number of matches |
| `DICOMInstancesTransferred.sent` | C-MOVE or C-GET finished; control API send | Operation, destination (C-MOVE), studies, Patient IDs, instances, failures |
| `SecurityAlert.association-rejected` | Calling AE refused by the allow-list | Calling AE, peer address |
| `PatientRecord.conflict-detected` | Patient conflict on C-STORE | Kind, policy, action, study, series, SOP instance, existing and incoming Patient IDs |
| `PatientRecord.conflict-resolved` and other control API events | Administrator actions through the control API | See [control-api-v1.md](control-api-v1.md) |

#### 11.2.3 Audit Trail Message Transmission Profile - SYSLOG - TLS - N/A

N/A.

#### 11.2.4 Audit Trail Message Transmission Profile - SYSLOG - UDP - N/A

N/A.

#### 11.2.5 Secure Transport Connection Details

| Item | Value |
|---|---|
| TLS versions | 1.2 and 1.3 (1.1 and older refused; evidence 60) |
| Cipher suites | Go `crypto/tls` defaults. Negotiated in the test: `ECDHE-ECDSA-AES128-GCM-SHA256` (TLS 1.2), `TLS_AES_128_GCM_SHA256` (TLS 1.3; evidence 40, 60) |
| Server certificate | PEM files (`tls_cert_file`, `tls_key_file`). `libredcm -gen-cert DIR` writes a self-signed ECDSA P-256 certificate valid 825 days and keeps existing files unless forced (DCM-SRS-042) |
| Client certificate | Not requested |
| TLS for outgoing associations | Not supported (C-MOVE sub-operations are plain); the C-ECHO test can use TLS without verifying the peer |

#### 11.2.6 Attribute Confidentiality Details - N/A

N/A.

#### 11.2.7 Digital Signature Details - N/A

N/A.

#### 11.2.8 Additional DICOM Security Profile Details - N/A

N/A.

## 12 Mapping of Attributes - N/A

N/A. LibreDCM has no Modality Worklist or MPPS.
