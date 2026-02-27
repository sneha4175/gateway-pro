# COMP6721 Applied Artificial Intelligence Project

## Project Description
This repository contains the implementation and documentation for the COMP6721 course project. The goal is to classify indoor and outdoor images using machine learning techniques, including **Random Forests**, **Decision Trees (Supervised and semi-supervised)**, **Boosting**, and **Convolutional Neural Networks (CNN)**. The project compares model performance on the Places MIT Museum dataset.

## Table of Contents
- [Installation](#installation)
- [Dataset](#dataset)
- [Directory Structure](#directory-structure)
- [Usage](#usage)
- [Results](#results)
- [Contributions](#contributions)
- [Contact](#contact)

## Installation
### Prerequisites
- Python 3.7+
- Google Colab (suggested for GPU acceleration)

### Dependencies
Install required libraries using:
```bash
pip install numpy scikit-learn matplotlib seaborn pillow torch torchvision
```

## Dataset
### Source
Places MIT Museum Dataset:
- Download from [MIT Places Dataset](http://places.csail.mit.edu/browser.html)
- Training Data: `museum-indoor` (8119 images), `museum-outdoor` (4221 images)
- Validation Data: Balanced subset for evaluation

### Preprocessing
- Images resized to **64x64 pixels** and converted to **RGB arrays**
- Dataset structure:
  ```
  dataset/
    Museum_Training/
      Training/
        museum-indoor/
        museum-outdoor/
    Museum_Test/
      Museum_Validation/
        museum-indoor/
        museum-outdoor/
  ```

### Sample Dataset
- A small subset of images (`sample_dataset/`) is included for validation
- Contains **50 indoor** and **50 outdoor** images

## Directory Structure
```
COMP6721_Project/
├── src/
│   ├── random_forest.py
│   ├── decision_tree_supervised.py
│   ├── decision_tree_semi_supervised.py
│   ├── boosting.py
│   └── cnn.py  # Phase 2 Implementation
├── dataset/
│   ├── dataset_description.md
│   └── sample_dataset/
├── requirements.txt
├── README.md
└── pretrained_models/
```

## Usage
### Running Scripts
- **Random Forest Classifier**:
  ```bash
  python src/random_forest.py
  ```

- **Decision Tree (Supervised)**:
  ```bash
  python src/decision_tree_supervised.py
  ```

- **Decision Tree (Semi-Supervised)**:
  ```bash
  python src/decision_tree_semi_supervised.py
  ```

- **Boosting Classifier**:
  ```bash
  python src/boosting.py
  ```

- **CNN (Phase 2)**:
  ```bash
  python src/cnn.py
  ```

### Output
- Performance metrics (**accuracy**, **precision**, **recall**, **F1-score**) and **confusion matrices** are printed
- Visualizations saved in `outputs/`
- CNN-specific outputs include **training curves**, **validation metrics**, and **model architecture diagrams**

## Results
### Key Findings
- CNN outperforms traditional machine learning models on the Places MIT Dataset
- Semi-supervised learning shows improved performance over supervised-only approaches
- Detailed results and analysis available in the [Project Report](#note)

### Note
For full results and analysis, refer to the project report submitted via Moodle.

## Contributions
### **Sneha Khoreja**
- **Phase 1**: 
  - Dataset preprocessing & validation
  - Model training (Random Forest, Boosting)
  - Report writing (methodology, experimental setup)
- **Phase 2**: 
  - CNN architecture development (CNN1)
  - Hyperparameter tuning
  - Model training & optimization
  - Report writing (CNN methodology and results)

### **Siya Patel**
- **Phase 1**: 
  - Performance metrics & visualization
  - Semi-Supervised Decision Tree implementation
  - Code optimization & testing
- **Phase 2**: 
  - Advanced CNN architecture (CNN2)
  - Model evaluation & visualization
  - Code implementation & testing
  - Report writing (CNN performance analysis)

## Contact
For questions, email:
- snehakhoreja1710@gmail.com
- siyapatel270702@gmail.com

## About
This repository contains the implementation and documentation for the COMP6721 course project, focusing on indoor/outdoor image classification using supervised and semi-supervised machine learning models including Random Forest, Decision Trees, Boosting, and CNNs.
